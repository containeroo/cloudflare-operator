package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/template"
)

type DNSRecord struct {
	Name    string
	Type    string
	TTL     string
	Content string
}

var zonefilePath string

const zonefileParseRegex string = `(.*)\s(\d+)\sIN\s([A-Z]+)\s(.*)`

//go:embed dnsrecord.yaml.tmpl
var dnsrecordTemplate embed.FS

func init() {
	flag.StringVar(&zonefilePath, "file", "", "Path to the exported zonefile")
	flag.Parse()
}

func run(out io.Writer) error {
	zonefile, err := os.ReadFile(zonefilePath)
	if err != nil {
		return err
	}

	regex := regexp.MustCompile(zonefileParseRegex)

	var records []DNSRecord

	for _, line := range strings.Split(string(zonefile), "\n") {
		if match := regex.MatchString(line); match {
			name := strings.TrimSuffix(regex.FindStringSubmatch(line)[1], ".")
			ttl := regex.FindStringSubmatch(line)[2]
			recordType := regex.FindStringSubmatch(line)[3]
			content := regex.FindStringSubmatch(line)[4]

			if recordType == "SOA" || recordType == "NS" {
				continue
			}

			record := DNSRecord{
				Name:    name,
				Type:    recordType,
				TTL:     ttl,
				Content: content,
			}

			records = append(records, record)
		}
	}

	funcMap := template.FuncMap{
		"split": strings.Split,
		"trimDot": func(s string) string {
			return strings.TrimSuffix(s, ".")
		},
		"cleanName": func(s string) string {
			noDots := strings.ReplaceAll(s, ".", "-")
			noUnderscores := strings.ReplaceAll(noDots, "_", "-")
			return noUnderscores
		},
	}

	tmpl, err := template.New("dnsrecord.yaml.tmpl").Funcs(funcMap).ParseFS(dnsrecordTemplate, "dnsrecord.yaml.tmpl")

	for _, record := range records {
		if err != nil {
			return err
		}

		err = tmpl.Execute(out, record)
		if err != nil {
			return err
		}
	}

	return nil
}

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}
