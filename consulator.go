package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

var (
	debug   = flag.Bool("debug", false, "Show debugging information")
	dump    = flag.Bool("dump", false, "Dump loaded data as JSON, suitable for using in a 'consul kv import'")
	format  = flag.String("format", "", "Specify data format(json or yaml) when reading from stdin.")
	prefix  = flag.String("prefix", "", "Specifies a Consul tree to work under.")
	glue    = flag.String("glue", "\n", "Glue to use when joining array values")
	sync    = flag.Bool("sync", false, "Sync to consul")
	trace   = flag.Bool("trace", false, "Show even more debugging information")
	quiet   = flag.Bool("quiet", false, "Only show errors")
	enc     = json.NewEncoder(os.Stdout)
	path    string
	absPath string
	nArgs   int
	data    map[string][]byte
	Trace   *log.Logger
	Debug   *log.Logger
	Info    *log.Logger
	Warning *log.Logger
	Error   *log.Logger
	usage   = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] [PATH]\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "PATH should be the path to a file or directory that contains your data.")
		fmt.Fprintln(os.Stderr, "If no path is provided, stdin is used. In this case, -format must be specified.\n")
		fmt.Fprintln(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The usual Consul client environment variables can be used to configure the connection:\n")
		fmt.Fprintln(os.Stderr, " - CONSUL_HTTP_ADDR")
		fmt.Fprintln(os.Stderr, " - CONSUL_HTTP_TOKEN")
		fmt.Fprintln(os.Stderr, " - CONSUL_HTTP_SSL\n")
		fmt.Fprintln(os.Stderr, "Etc. See https://www.consul.io/docs/commands/ for a complete list.\n")
	}
)

func main() {
	flag.Usage = usage
	flag.Parse()
	// clean up prefix
	*prefix = strings.TrimSuffix(strings.TrimPrefix(*prefix, "/"), "/")
	if *prefix != "" {
		*prefix += "/"
	}
	nArgs = flag.NArg()
	logInit()
	data = make(map[string][]byte)
	switch nArgs {
	case 0:
		// use stdin
		Trace.Println("No arguments, using stdin instead")
		fi, _ := os.Stdin.Stat()
		switch *format {
		case "json":
			fallthrough
		case "yaml":
			Info.Printf("Looking for %s data on stdin..", *format)
			err := parseConfig(fmt.Sprintf(".%v", *format), fi, nil)
			if err != nil {
				Error.Printf("%v: %v\n", path, err)
			}
		default:
			Error.Println("When reading from stdin, the -format option must be provided and must be one of: json, yaml\n")
			usage()
			os.Exit(1)
		}
	case 1:
		path = flag.Arg(0)
		absPath, _ = filepath.Abs(path)
		Info.Printf("Looking for data at %s..", absPath)
		_, err := os.Stat(absPath)
		if err != nil {
			Error.Fatal(err)
		}
		err = filepath.Walk(absPath, parseConfig)
		if err != nil {
			Error.Fatal(err)
		}
	default:
		Error.Printf("1 argument expected, but found %d\n\n", flag.NArg())
		usage()
		os.Exit(255)
	}
	Info.Printf("Found %d keys", len(data))
	if *dump {
		exportData()
	}
	if *sync {
		syncConsul()
	}
}

func parseConfig(path string, f os.FileInfo, err error) error {
	var fp *os.File
	Trace.Printf("Traversing %s", path)
	if f.Mode().IsRegular() && nArgs == 1 {
		fp, err = os.Open(path)
	} else if nArgs == 0 {
		fp = os.Stdin
	} else {
		return nil
	}
	if err != nil {
		Warning.Printf("%v: %v\n", path, err)
	}
	keyPrefix := strings.Split(
		// remove leading '/'
		strings.TrimPrefix(
			// remove the file extension
			strings.TrimSuffix(
				// remove the base path that was passed as -path
				strings.TrimPrefix(path, absPath),
				filepath.Ext(path)),
			string(os.PathSeparator)),
		string(os.PathSeparator))
	if keyPrefix[0] == "" {
		// remove the "" value if passed a file directly in -path
		keyPrefix = []string{}
	}
	Debug.Printf("keyprefix is %v", keyPrefix)
	switch {
	case strings.HasSuffix(strings.ToLower(path), ".json"):
		Debug.Printf("Parsing %s as json", path)
		err := parseJson(fp, keyPrefix)
		if err != nil {
			Error.Fatalf("%v: %v\n", path, err)
		}
	case strings.HasSuffix(strings.ToLower(path), ".yml"):
		fallthrough
	case strings.HasSuffix(strings.ToLower(path), ".yaml"):
		Debug.Printf("Parsing %s as yaml", path)
		// yaml handling based on https://github.com/bronze1man/yaml2json
		yamlR, yamlW := io.Pipe()
		go func() {
			defer yamlW.Close()
			err := yamlToJson(fp, yamlW)
			if err != nil {
				Warning.Printf("%v: %v\n", path, err)
			}
		}()
		err := parseJson(yamlR, keyPrefix)
		if err != nil {
			Warning.Printf("%v: %v\n", path, err)
		}
	case strings.HasSuffix(strings.ToLower(path), ".properties"):
		Debug.Printf("Parsing %s as properties", path)
	case strings.HasSuffix(strings.ToLower(path), ".ini"):
		Debug.Printf("Parsing %s as ini", path)
	default:
	}
	return nil
}

func exportData() {
	exported := make([]*kvExportEntry, len(data))
	i := 0
	for key, val := range data {
		exported[i] = toExportEntry(key, val)
		i++
	}
	json, err := json.MarshalIndent(exported, "", "\t")
	if err != nil {
		Error.Fatalf("Error exporting data: %s", err)
	}
	os.Stdout.Write(json)
	fmt.Println("")
}
