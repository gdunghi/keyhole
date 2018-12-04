// Copyright 2018 Kuei-chun Chen. All rights reserved.

package ftdc

import (
	"io/ioutil"
	"testing"
)

func TestReadMetricsSummary(t *testing.T) {
	var err error
	var buffer []byte
	filename := "../test_data/diagnostic.data/metrics.2017-10-12T20-08-53Z-00000"
	if buffer, err = ioutil.ReadFile(filename); err != nil {
		t.Fatal(err)
	}
	m := NewMetrics()
	m.ReadMetricsSummary(buffer)
	if len(m.Blocks) != 164 {
		t.Fatal()
	}
}

func TestReadAllMetrics(t *testing.T) {
	var err error
	var buffer []byte
	filename := "../test_data/diagnostic.data/metrics.2017-10-12T20-08-53Z-00000"
	if buffer, err = ioutil.ReadFile(filename); err != nil {
		t.Fatal(err)
	}
	m := NewMetrics()
	m.ReadAllMetrics(buffer)
	if len(m.Blocks) != len(m.Data) {
		t.Fatal()
	}
}