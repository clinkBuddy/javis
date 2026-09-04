//go:build !windows

package metrics

import "errors"

var errNotWindows = errors.New("metrics: process sampling is only implemented on Windows")

func sampleProcess(uint32, int64) (rawProc, error) { return rawProc{}, errNotWindows }
func sampleHost(string) (rawHost, error)           { return rawHost{}, errNotWindows }
