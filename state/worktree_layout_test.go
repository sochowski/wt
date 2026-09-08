package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func TestPlacementOptions(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		valid bool
	}{
		{nil, true}, {[]string{"--placement", "window"}, true},
		{[]string{"--placement", "split", "--anchor", "focused", "--direction", "stack"}, true},
		{[]string{"--placement", "split", "--direction", "above"}, false},
		{[]string{"--placement", "split", "--size", "35"}, false},
		{[]string{"--size", "35"}, false}, {[]string{"--anchor", "caller"}, false},
		{[]string{"--placement", "split", "--size", "0"}, false},
		{[]string{"--placement", "split", "--size", "100"}, false},
		{[]string{"--placement", "split", "--size", "-1"}, false},
		{[]string{"--placement", "split", "--size", "50%"}, false},
		{[]string{"--placement", "split", "--direction", "diagonal"}, false},
		{[]string{"--placement", "other"}, false}, {[]string{"unexpected"}, false},
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		p := placementFlags(fs, "window")
		err := parsePlacement(fs, p, tc.args)
		if (err == nil) != tc.valid {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
}

func TestStackLayout(t *testing.T) {
	layout, err := stackLayout(101, 40, 60, []string{"%1", "%2", "%3", "%4"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(layout, "101x40,0,0{60x40,0,0,1,40x40,61,0[40x13,61,0,2,40x13,61,14,3,40x12,61,28,4]}") {
		t.Fatal(layout)
	}
	if _, err = stackLayout(80, 2, 60, []string{"%1", "%2", "%3"}); err == nil {
		t.Fatal("tiny window accepted")
	}
}
