// Copyright 2018 Kuei-chun Chen. All rights reserved.

package util

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// COLLSCAN constance
const COLLSCAN = "COLLSCAN"

// LogInfo keeps loginfo struct
type LogInfo struct {
	OpsPatterns    []OpPerformanceDoc
	OutputFilename string
	SlowOps        []SlowOps
	collscan       bool
	filename       string
	mongoInfo      string
	silent         bool
	verbose        bool
}

// OpPerformanceDoc stores performance data
type OpPerformanceDoc struct {
	Command    string // count, delete, find, remove, and update
	Count      int    // number of ops
	Filter     string // query pattern
	MaxMilli   int    // max millisecond
	Namespace  string // database.collectin
	Scan       string // COLLSCAN
	TotalMilli int    // total milliseconds
	Index      string // index used
}

// SlowOps holds slow ops log and time
type SlowOps struct {
	Milli int
	Log   string
}

// NewLogInfo -
func NewLogInfo(filename string) *LogInfo {
	li := LogInfo{filename: filename, collscan: false, silent: false, verbose: false}
	li.OutputFilename = filepath.Base(filename)
	if strings.HasSuffix(li.OutputFilename, ".gz") {
		li.OutputFilename = li.OutputFilename[:len(li.OutputFilename)-3]
	}
	li.OutputFilename += ".enc"
	return &li
}

// SetCollscan -
func (li *LogInfo) SetCollscan(collscan bool) {
	li.collscan = collscan
}

// SetSilent -
func (li *LogInfo) SetSilent(silent bool) {
	li.silent = silent
}

// SetVerbose -
func (li *LogInfo) SetVerbose(verbose bool) {
	li.verbose = verbose
}

// GetDocByField get JSON string by a field
func GetDocByField(str string, field string) string {
	i := strings.Index(str, field)
	if i < 0 {
		return ""
	}
	str = strings.Trim(str[i+len(field):], " ")
	isFound := false
	bpos := 0 // begin position
	epos := 0 // end position
	for _, r := range str {
		epos++
		if isFound == false && r == '{' {
			isFound = true
			bpos++
		} else if isFound == true {
			if r == '{' {
				bpos++
			} else if r == '}' {
				bpos--
			}
		}

		if isFound == true && bpos == 0 {
			break
		}
	}
	return str[bpos:epos]
}

func getConfigOptions(reader *bufio.Reader) []string {
	matched := regexp.MustCompile(`^\S+ .? CONTROL\s+\[\w+\] (\w+(:)?) (.*)$`)
	var err error
	var buf []byte
	var strs []string

	for {
		buf, _, err = reader.ReadLine() // 0x0A separator = newline
		if err != nil {
			break
		} else if matched.MatchString(string(buf)) == true {
			result := matched.FindStringSubmatch(string(buf))
			if result[1] == "db" {
				s := "db " + result[3]
				strs = append(strs, s)
			} else if result[1] == "options:" {
				re := regexp.MustCompile(`((\S+):)`)
				body := re.ReplaceAllString(result[3], "\"$1\":")
				var buf bytes.Buffer
				json.Indent(&buf, []byte(body), "", "  ")

				strs = append(strs, "config options:")
				strs = append(strs, string(buf.Bytes()))
				return strs
			}
		}
	}
	return strs
}

// Analyze -
func (li *LogInfo) Analyze() (string, error) {
	err := li.Parse()
	if err != nil {
		return "", err
	}
	summaries := []string{}
	if li.verbose == true {
		summaries = append([]string{}, li.mongoInfo)
	}
	if len(li.SlowOps) > 0 {
		summaries = append(summaries, fmt.Sprintf("Ops slower than 10 seconds (list top %d):", len(li.SlowOps)))
		for _, op := range li.SlowOps {
			summaries = append(summaries, MilliToTimeString(float64(op.Milli))+" => "+op.Log)
		}
		summaries = append(summaries, "\n")
	}
	summaries = append(summaries, printLogsSummary(li.OpsPatterns))
	var data bytes.Buffer
	enc := gob.NewEncoder(&data)
	if err = enc.Encode(li); err != nil {
		log.Println("encode error:", err)
	}
	ioutil.WriteFile(li.OutputFilename, data.Bytes(), 0644)
	return strings.Join(summaries, "\n"), nil
}

// Parse -
func (li *LogInfo) Parse() error {
	var err error
	var reader *bufio.Reader
	var file *os.File
	var opsMap map[string]OpPerformanceDoc

	opsMap = make(map[string]OpPerformanceDoc)
	if file, err = os.Open(li.filename); err != nil {
		return err
	}
	defer file.Close()

	if reader, err = NewReader(file); err != nil {
		return err
	}
	lineCounts, _ := CountLines(reader)

	file.Seek(0, 0)
	reader, _ = NewReader(file)
	var buffer bytes.Buffer
	if strs := getConfigOptions(reader); len(strs) > 0 {
		for _, s := range strs {
			buffer.WriteString(s + "\n")
		}
	}
	li.mongoInfo = buffer.String()

	matched := regexp.MustCompile(`^\S+ \S+\s+(\w+)\s+\[\w+\] (\w+) (\S+) \S+: (.*) (\d+)ms$`) // SERVER-37743
	file.Seek(0, 0)
	if reader, err = NewReader(file); err != nil {
		return err
	}
	index := 0
	for {
		if index%25 == 1 && li.silent == false {
			fmt.Fprintf(os.Stderr, "\r%3d%% ", (100*index)/lineCounts)
		}
		var buf []byte
		var isPrefix bool
		buf, isPrefix, err = reader.ReadLine() // 0x0A separator = newline
		str := string(buf)
		for isPrefix == true {
			var bbuf []byte
			bbuf, isPrefix, err = reader.ReadLine()
			str += string(bbuf)
		}
		index++
		scan := ""
		if err != nil {
			break
		} else if matched.MatchString(str) == true {
			if strings.Index(str, "COLLSCAN") >= 0 {
				scan = COLLSCAN
			}
			if li.collscan == true && scan != COLLSCAN {
				continue
			}
			result := matched.FindStringSubmatch(str)
			isFound := false
			bpos := 0 // begin position
			epos := 0 // end position
			for _, r := range result[4] {
				epos++
				if isFound == false && r == '{' {
					isFound = true
					bpos++
				} else if isFound == true {
					if r == '{' {
						bpos++
					} else if r == '}' {
						bpos--
					}
				}

				if isFound == true && bpos == 0 {
					break
				}
			}

			re := regexp.MustCompile(`^(\w+) ({.*})$`)
			op := result[2]
			ns := result[3]
			if ns == "local.oplog.rs" || strings.HasSuffix(ns, ".$cmd") == true {
				continue
			}
			filter := result[4][:epos]
			ms := result[5]
			if op == "command" {
				idx := strings.Index(filter, "command: ")
				if idx > 0 {
					filter = filter[idx+len("command: "):]
				}
				res := re.FindStringSubmatch(filter)
				if len(res) < 3 {
					continue
				}
				op = res[1]
				filter = res[2]
			}

			if hasFilter(op) == false {
				continue
			}
			if op == "delete" && strings.Index(filter, "writeConcern:") >= 0 {
				continue
			} else if op == "find" {
				nstr := "{ }"
				s := GetDocByField(filter, "filter: ")
				if s != "" {
					nstr = s
				}
				s = GetDocByField(filter, "sort: ")
				if s != "" {
					nstr = nstr + ", sort: " + s
				}
				filter = nstr
			} else if op == "count" || op == "distinct" {
				nstr := ""
				s := GetDocByField(filter, "query: ")
				if s != "" {
					nstr = s
				}
				filter = nstr
			} else if op == "delete" || op == "update" || op == "remove" {
				var s string
				// if result[1] == "WRITE" {
				if strings.Index(filter, "query: ") >= 0 {
					s = GetDocByField(filter, "query: ")
				} else {
					s = GetDocByField(filter, "q: ")
				}
				if s != "" {
					filter = s
				}
			} else if op == "aggregate" || (op == "getmore" && strings.Index(filter, "pipeline:") > 0) {
				nstr := ""
				s := ""
				for _, mstr := range []string{"pipeline: [ { $match: ", "pipeline: [ { $sort: "} {
					s = GetDocByField(result[4], mstr)
					if s != "" {
						nstr = s
						filter = nstr
						break
					}
				}
				if s == "" {
					if scan == "COLLSCAN" { // it's a collection scan without $match or $sort
						filter = "{}"
					} else {
						continue
					}
				}
			} else if op == "getMore" || op == "getmore" {
				nstr := ""
				s := GetDocByField(result[4], "originatingCommand: ")

				if s != "" {
					for _, mstr := range []string{"filter: ", "pipeline: [ { $match: ", "pipeline: [ { $sort: "} {
						s = GetDocByField(result[4], mstr)
						if s != "" {
							nstr = s
							filter = nstr
							break
						}
					}
					if s == "" {
						continue
					}
				} else {
					continue
				}
			}
			index := GetDocByField(str, "planSummary: IXSCAN")
			if index == "" && strings.Index(str, "planSummary: EOF") >= 0 {
				index = "EOF"
			}
			if index == "" && strings.Index(str, "planSummary: IDHACK") >= 0 {
				index = "IDHACK"
			}
			if scan == "" && strings.Index(str, "planSummary: COUNT_SCAN") >= 0 {
				index = "COUNT_SCAN"
			}
			filter = removeInElements(filter, "$in: [ ")
			filter = removeInElements(filter, "$nin: [ ")
			filter = removeInElements(filter, "$in: [ ")
			filter = removeInElements(filter, "$nin: [ ")

			isRegex := strings.Index(filter, "{ $regex: ")
			if isRegex >= 0 {
				cnt := 0
				for _, r := range filter[isRegex:] {
					if r == '}' {
						break
					}
					cnt++
				}
				filter = filter[:(isRegex+10)] + "/.../.../" + filter[(isRegex+cnt):]
			}

			re = regexp.MustCompile(`(: "[^"]*"|: -?\d+(\.\d+)?|: new Date\(\d+?\)|: true|: false)`)
			filter = re.ReplaceAllString(filter, ":1")
			re = regexp.MustCompile(`, shardVersion: \[.*\]`)
			filter = re.ReplaceAllString(filter, "")
			re = regexp.MustCompile(`( ObjectId\('\S+'\))|(UUID\("\S+"\))|( Timestamp\(\d+, \d+\))|(BinData\(\d+, \S+\))`)
			filter = re.ReplaceAllString(filter, "1")
			re = regexp.MustCompile(`(: \/.*\/(.?) })`)
			filter = re.ReplaceAllString(filter, ": /regex/$2}")
			filter = strings.Replace(strings.Replace(filter, "{ ", "{", -1), " }", "}", -1)
			key := op + "." + filter + "." + scan
			_, ok := opsMap[key]
			milli, _ := strconv.Atoi(ms)
			if milli >= 10000 { // >= 10 seconds too slow, top 10
				li.SlowOps = append(li.SlowOps, SlowOps{Milli: milli, Log: str})
				if len(li.SlowOps) > 10 {
					sort.Slice(li.SlowOps, func(i, j int) bool {
						return li.SlowOps[i].Milli > li.SlowOps[j].Milli
					})
					li.SlowOps = li.SlowOps[:10]
				}
			}
			if ok {
				max := opsMap[key].MaxMilli
				if milli > max {
					max = milli
				}
				x := opsMap[key].TotalMilli + milli
				y := opsMap[key].Count + 1
				opsMap[key] = OpPerformanceDoc{Command: opsMap[key].Command, Namespace: ns, Filter: opsMap[key].Filter, MaxMilli: max, TotalMilli: x, Count: y, Scan: scan, Index: index}
			} else {
				opsMap[key] = OpPerformanceDoc{Command: op, Namespace: ns, Filter: filter, TotalMilli: milli, MaxMilli: milli, Count: 1, Scan: scan, Index: index}
			}
		}
	}

	li.OpsPatterns = make([]OpPerformanceDoc, 0, len(opsMap))
	for _, value := range opsMap {
		li.OpsPatterns = append(li.OpsPatterns, value)
	}
	sort.Slice(li.OpsPatterns, func(i, j int) bool {
		return float64(li.OpsPatterns[i].TotalMilli)/float64(li.OpsPatterns[i].Count) > float64(li.OpsPatterns[j].TotalMilli)/float64(li.OpsPatterns[j].Count)
	})
	if li.silent == false {
		fmt.Fprintf(os.Stderr, "\r     \r")
	}
	return nil
}

func printLogsSummary(arr []OpPerformanceDoc) string {
	var buffer bytes.Buffer
	buffer.WriteString("\r+---------+--------+------+--------+------+---------------------------------+--------------------------------------------------------------+\n")
	buffer.WriteString(fmt.Sprintf("| Command |COLLSCAN|avg ms| max ms | Count| %-32s| %-60s |\n", "Namespace", "Query Pattern"))
	buffer.WriteString("|---------+--------+------+--------+------+---------------------------------+--------------------------------------------------------------|\n")
	for _, value := range arr {
		str := value.Filter
		if len(value.Command) > 13 {
			value.Command = value.Command[:13]
		}
		if len(value.Namespace) > 33 {
			length := len(value.Namespace)
			value.Namespace = value.Namespace[:1] + "*" + value.Namespace[(length-31):]
		}
		if len(str) > 60 {
			str = value.Filter[:60]
			idx := strings.LastIndex(str, " ")
			str = value.Filter[:idx]
		}
		output := ""
		avg := float64(value.TotalMilli) / float64(value.Count)
		avgstr := MilliToTimeString(avg)
		if value.Scan == COLLSCAN {
			output = fmt.Sprintf("|%-9s \x1b[31;1m%8s\x1b[0m %6s %8d %6d %-33s \x1b[31;1m%-62s\x1b[0m|\n", value.Command, value.Scan,
				avgstr, value.MaxMilli, value.Count, value.Namespace, str)
		} else {
			output = fmt.Sprintf("|%-9s \x1b[31;1m%8s\x1b[0m %6s %8d %6d %-33s %-62s|\n", value.Command, value.Scan,
				avgstr, value.MaxMilli, value.Count, value.Namespace, str)
		}
		buffer.WriteString(output)
		if len(value.Filter) > 60 {
			remaining := value.Filter[len(str):]
			for i := 0; i < len(remaining); i += 60 {
				epos := i + 60
				var pstr string
				if epos > len(remaining) {
					epos = len(remaining)
					pstr = remaining[i:epos]
				} else {
					str = strings.Trim(remaining[i:epos], " ")
					idx := strings.LastIndex(str, " ")
					if idx > 0 {
						pstr = str[:idx]
						i -= (60 - idx)
					}
				}
				if value.Scan == COLLSCAN {
					output = fmt.Sprintf("|%73s   \x1b[31;1m%-62s\x1b[0m|\n", " ", pstr)
					buffer.WriteString(output)
				} else {
					output = fmt.Sprintf("|%73s   %-62s|\n", " ", pstr)
					buffer.WriteString(output)
				}
				buffer.WriteString(output)
			}
		}
		if value.Index != "" {
			output = fmt.Sprintf("|...index: \x1b[32;1m%-128s\x1b[0m|\n", value.Index)
			buffer.WriteString(output)
		}
	}
	buffer.WriteString("+---------+--------+------+--------+------+---------------------------------+--------------------------------------------------------------+\n")
	return buffer.String()
}

// convert $in: [...] to $in: [ ]
func removeInElements(str string, instr string) string {
	idx := strings.Index(str, instr)
	if idx < 0 {
		return str
	}

	idx += len(instr) - 1
	cnt, epos := -1, -1
	for _, r := range str {
		if cnt < idx {
			cnt++
			continue
		}
		if r == ']' {
			epos = cnt
			break
		}
		cnt++
	}

	if epos == -1 {
		str = str[:idx] + "...]"
	} else {
		str = str[:idx] + "..." + str[epos:]
	}
	return str
}

var filters = []string{"count", "delete", "find", "remove", "update", "aggregate", "getMore", "getmore"}

func hasFilter(op string) bool {
	for _, f := range filters {
		if f == op {
			return true
		}
	}
	return false
}

// MilliToTimeString converts milliseconds to time string, e.g. 1.5m
func MilliToTimeString(milli float64) string {
	avgstr := fmt.Sprintf("%6.0f", milli)
	if milli >= 3600000 {
		milli /= 3600000
		avgstr = fmt.Sprintf("%4.1fh", milli)
	} else if milli >= 60000 {
		milli /= 60000
		avgstr = fmt.Sprintf("%3.1fm", milli)
	} else if milli >= 1000 {
		milli /= 1000
		avgstr = fmt.Sprintf("%3.1fs", milli)
	}
	return avgstr
}
