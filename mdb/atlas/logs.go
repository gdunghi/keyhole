// Copyright 2019 Kuei-chun Chen. All rights reserved.

package atlas

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/simagix/keyhole/sim/util"
)

// Logs info
type Logs struct {
	apiKey      string
	clusterName string
	endUnix     int64
	err         error
	groupID     string
	startUnix   int64
	userName    string
	verbose     bool
}

// ParseAtlasURI parses atlas://user:apiKey@groupID/cluster
func ParseAtlasURI(uri string) *Logs {
	matched := regexp.MustCompile(`^atlas:\/\/(\S+):(\S+)@([^\/]+)\/([^\/]+)(\/([12]\d{3}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])))?`)
	if matched.MatchString(uri) == false {
		return &Logs{err: errors.New(`Incorrect format, should be "atlas://user:apiKey@groupID/clusterName[/yyyy-mm-dd]"`)}
	}

	results := matched.FindStringSubmatch(uri)
	t := time.Now()
	endUnix := t.Unix()
	startUnix := t.Add(time.Hour * -24).Unix()
	if len(results) > 6 && results[6] != "" {
		t, _ = time.Parse("2006-01-02", results[6])
		startUnix = t.Add(time.Hour * -24).Unix()
		endUnix = t.Add(time.Hour * 24).Unix()
	}
	return &Logs{userName: results[1], apiKey: results[2], groupID: results[3],
		clusterName: results[4], startUnix: startUnix, endUnix: endUnix}
}

// SetVerbose -
func (lg *Logs) SetVerbose(verbose bool) {
	lg.verbose = verbose
}

// Error returns error string
func (lg *Logs) Error() string {
	if lg.err != nil {
		return lg.err.Error()
	}

	return ""
}

// DownloadLogs download all logs of a groupID
func (lg *Logs) DownloadLogs(dirname string) ([]string, error) {
	var err error
	var filenames []string

	su := NewSummary(lg.userName + ":" + lg.apiKey)
	var processes []interface{}
	if processes, err = su.getProcesses(lg.groupID); err != nil {
		return filenames, err
	}
	for _, process := range processes {
		maps := process.(map[string]interface{})
		if strings.Index(strings.ToLower(maps["hostname"].(string)), strings.ToLower(lg.clusterName+"-")) == 0 &&
			strings.Index(maps["typeName"].(string), "REPLICA_") == 0 {
			var filename string
			if filename, err = lg.downloadLog(dirname, maps["hostname"].(string), lg.startUnix, lg.endUnix); err == nil {
				filenames = append(filenames, filename)
			}
		}
	}
	return filenames, err
}

func (lg *Logs) downloadLog(dirname string, hostname string, startUnix int64, endUnix int64) (string, error) {
	var err error
	var resp *http.Response
	var b []byte
	var filename = dirname + "/mongodb.log." + hostname + ".gz"
	uri := BaseURL + "/groups/" + lg.groupID + "/clusters/" + hostname + "/logs/mongodb.gz"
	uri += "?startDate=" + fmt.Sprintf("%v", startUnix) + "&endDate=" + fmt.Sprintf("%v", endUnix)
	if lg.verbose {
		fmt.Println("download from", uri)
	}
	headers := map[string]string{}
	headers["Content-Type"] = ApplicationJSON
	headers["Accept"] = ApplicationGZip
	if resp, err = util.HTTPDigest("GET", uri, lg.userName, lg.apiKey, headers); err != nil {
		return filename, err
	}
	defer resp.Body.Close()
	if b, err = ioutil.ReadAll(resp.Body); err != nil {
		return filename, err
	}
	if _, err = os.Stat(filename); err == nil {
		os.Rename(filename, filename+"."+time.Now().Format(time.RFC3339))
	}
	err = ioutil.WriteFile(filename, b, 0644)
	return filename, err
}
