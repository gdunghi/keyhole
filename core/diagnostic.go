// Copyright 2018 Kuei-chun Chen. All rights reserved.

package keyhole

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/globalsign/mgo/bson"
	"github.com/simagix/keyhole/ftdc"
)

// OSDoc -
type OSDoc struct {
	Name    string `json:"name" bson:"name"`
	Type    string `json:"type" bson:"type"`
	Version string `json:"version" bson:"version"`
}

// SystemDoc -
type SystemDoc struct {
	CPUArch   string `json:"cpuArch" bson:"cpuArch"`
	Hostname  string `json:"hostname" bson:"hostname"`
	NumCores  int    `json:"numCores" bson:"numCores"`
	MemSizeMB int    `json:"memSizeMB" bson:"memSizeMB"`
}

// HostInfo -
type HostInfo struct {
	OS     OSDoc     `json:"os" bson:"os"`
	System SystemDoc `json:"system" bson:"system"`
}

// BuildInfo -
type BuildInfo struct {
	Version string `json:"version" bson:"version"`
}

// ServerInfoDoc -
type ServerInfoDoc struct {
	HostInfo  HostInfo  `json:"hostInfo" bson:"hostInfo"`
	BuildInfo BuildInfo `json:"buildInfo" bson:"buildInfo"`
}

// DiagnosticData -
type DiagnosticData struct {
	ServerInfo        interface{}
	ServerStatusList  []ServerStatusDoc
	ReplSetStatusList []ReplSetStatusDoc
	SystemMetricsList []SystemMetricsDoc
	span              int
}

// DiagnosticDoc -
type DiagnosticDoc struct {
	Start            time.Time        `json:"start" bson:"start"`
	ServerStatus     ServerStatusDoc  `json:"serverStatus" bson:"serverStatus"`
	ReplSetGetStatus ReplSetStatusDoc `json:"replSetGetStatus" bson:"replSetGetStatus"`
	SystemMetrics    SystemMetricsDoc `json:"systemMetrics" bson:"systemMetrics"`
	End              time.Time        `json:"end" bson:"end"`
}

// NewDiagnosticData -
func NewDiagnosticData(span int) *DiagnosticData {
	if span <= 0 {
		span = 300 // 5 minutes
	}
	return &DiagnosticData{ServerStatusList: []ServerStatusDoc{}, ReplSetStatusList: []ReplSetStatusDoc{}, span: span}
}

// PrintDiagnosticData prints diagnostic data of MongoD
func (d *DiagnosticData) PrintDiagnosticData(filenames []string, isWeb bool) (string, error) {
	var err error
	var fi os.FileInfo

	for _, filename := range filenames {
		if fi, err = os.Stat(filename); err != nil {
			return "", err
		}
		switch mode := fi.Mode(); {
		case mode.IsDir():
			if err = d.readDiagnosticDir(filename); err != nil {
				return "", err
			}
		case mode.IsRegular():
			filenames = []string{filename}
			if err = d.readDiagnosticFiles(filenames); err != nil {
				return "", err
			}
		}
	}

	if len(d.ServerStatusList) == 0 {
		return "No FTDC data found.", err
	}

	if isWeb == true {
		str := d.ServerStatusList[0].LocalTime.Format("2006-01-02T15:04:05Z") +
			" - " + d.ServerStatusList[len(d.ServerStatusList)-1].LocalTime.Format("2006-01-02T15:04:05Z")
		return str, err
	}

	if d.ServerInfo != nil {
		b, _ := json.MarshalIndent(d.ServerInfo, "", "  ")
		fmt.Println(string(b))
	}
	return PrintAllStats(d.ServerStatusList, -1), err
}

// readDiagnosticDir reads diagnotics.data from a directory
func (d *DiagnosticData) readDiagnosticDir(dirname string) error {
	var err error
	var files []os.FileInfo
	var filenames []string

	if files, err = ioutil.ReadDir(dirname); err != nil {
		return err
	}

	for _, f := range files {
		if strings.Index(f.Name(), "metrics.") != 0 && strings.Index(f.Name(), "keyhole_stats.") != 0 {
			continue
		}
		filename := dirname + "/" + f.Name()
		filenames = append(filenames, filename)
	}

	return d.readDiagnosticFiles(filenames)
}

// readDiagnosticFiles reads multiple files
func (d *DiagnosticData) readDiagnosticFiles(filenames []string) error {
	var err error

	if strings.Index(filenames[0], "keyhole_stats.") >= 0 {
		for _, filename := range filenames {
			d.analyzeServerStatus(filename)
		}
		return err
	}

	btime := time.Now()
	fmt.Println("reading", len(filenames), "files.")
	var diagDataMap = map[string]DiagnosticData{}
	nThreads := runtime.NumCPU()
	if nThreads < 4 {
		nThreads = 4
	}
	var wg = NewWaitGroup(nThreads) // use 4 threads to read
	for threadNum := 0; threadNum < len(filenames); threadNum++ {
		filename := filenames[threadNum]
		wg.Add(1)
		go func(threadNum int, filename string) {
			defer wg.Done()
			var diagData DiagnosticData
			if diagData, err = d.readDiagnosticFile(filename); err == nil {
				diagDataMap[strconv.Itoa(threadNum)] = diagData
			}
		}(threadNum, filename)
	}
	wg.Wait()
	for threadNum := 0; threadNum < len(filenames); threadNum++ {
		if diagDataMap[strconv.Itoa(threadNum)].ServerInfo != nil {
			d.ServerInfo = diagDataMap[strconv.Itoa(threadNum)].ServerInfo
		}
		d.ServerStatusList = append(d.ServerStatusList, diagDataMap[strconv.Itoa(threadNum)].ServerStatusList...)
		d.SystemMetricsList = append(d.SystemMetricsList, diagDataMap[strconv.Itoa(threadNum)].SystemMetricsList...)
		d.ReplSetStatusList = append(d.ReplSetStatusList, diagDataMap[strconv.Itoa(threadNum)].ReplSetStatusList...)
	}
	fmt.Println(len(filenames), "files loaded, time spent:", time.Now().Sub(btime))
	return err
}

// readDiagnosticFile reads diagnostic.data from a file
func (d *DiagnosticData) readDiagnosticFile(filename string) (DiagnosticData, error) {
	btm := time.Now()
	var diagData = DiagnosticData{}
	var buffer []byte
	var err error
	var pos uint32
	if buffer, err = ioutil.ReadFile(filename); err != nil {
		return diagData, err
	}
	var r io.ReadCloser
	var cnt int

	for {
		if pos >= uint32(len(buffer)) {
			break
		}
		bs := buffer[pos:(pos + 4)]
		var length = ftdc.GetUint32(bytes.NewReader(bs))

		bs = buffer[pos:(pos + length)]
		pos += length
		var out = bson.M{}
		if err = bson.Unmarshal(bs, &out); err != nil {
			log.Println(err)
			continue
		} else if out["type"] == 0 {
			diagData.ServerInfo = out["doc"]
		} else if out["type"] == 1 {
			bytesBuf := bytes.NewReader(out["data"].([]byte)[4:])
			// zlib decompress
			if r, err = zlib.NewReader(bytesBuf); err != nil {
				log.Println(err)
				continue
			}
			var data []byte
			if data, err = ioutil.ReadAll(r); err != nil {
				log.Println(err)
				continue
			}

			cnt++
			var dd DiagnosticData
			if dd, err = getDiagnosticData(data, d.span); err != nil {
				return diagData, err
			}
			diagData.ServerStatusList = append(diagData.ServerStatusList, dd.ServerStatusList...)
			diagData.SystemMetricsList = append(diagData.SystemMetricsList, dd.SystemMetricsList...)
			diagData.ReplSetStatusList = append(diagData.ReplSetStatusList, dd.ReplSetStatusList...)
		} else {
			log.Println("==>", out["type"])
		}
	}
	filename = strings.TrimRight(filename, "/")
	i := strings.LastIndex(filename, "/")
	if i >= 0 {
		filename = filename[i+1:]
	}
	fmt.Println("->", filename, "chunks:", cnt, ", time:", time.Now().Sub(btm))
	return diagData, err
}

// analyzeServerStatus -
func (d *DiagnosticData) analyzeServerStatus(filename string) error {
	var err error
	var file *os.File
	var reader *bufio.Reader
	var allDocs = []ServerStatusDoc{}
	var docs = []ServerStatusDoc{}
	var allRepls = []ReplSetStatusDoc{}
	var repls = []ReplSetStatusDoc{}

	if file, err = os.Open(filename); err != nil {
		return err
	}
	defer file.Close()

	if reader, err = NewReader(file); err != nil {
		return err
	}

	for {
		line, ferr := reader.ReadBytes('\n')
		if ferr == io.EOF {
			break
		}

		docs = []ServerStatusDoc{}
		if err = json.Unmarshal(line, &docs); err == nil {
			if len(docs) > 0 && docs[0].Host != "" {
				allDocs = append(allDocs, docs...)
			} else if err = json.Unmarshal(line, &repls); err == nil { // ReplSetStatusDoc
				allRepls = append(allRepls, repls...)
			}
		}
	}

	if len(allDocs) == 0 && len(allRepls) == 0 {
		return errors.New("No doc found")
	}

	d.ServerStatusList = append(d.ServerStatusList, allDocs...)
	d.ReplSetStatusList = append(d.ReplSetStatusList, allRepls...)
	return err
}

// systemMetrics
// end
// start
// serverStatus
// replSetGetStatus
// local.oplog.rs.stats
func unmarshalFirstBsonDoc(data []byte) DiagnosticData {
	var diagData = DiagnosticData{}
	var doc DiagnosticDoc
	bson.Unmarshal(data, &doc) // first document
	diagData.ServerStatusList = append(diagData.ServerStatusList, doc.ServerStatus)
	diagData.SystemMetricsList = append(diagData.SystemMetricsList, doc.SystemMetrics)
	diagData.ReplSetStatusList = append(diagData.ReplSetStatusList, doc.ReplSetGetStatus)
	return diagData
}
