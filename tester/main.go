/* Package tester can be used to perform repetition tests */
package tester

// #cgo CFLAGS: -g -Wall
// #include <stdlib.h>
// #include "timer.h"
import "C"

import (
	"fmt"
	"slices"
	"time"
)

type status int

const (
	starting status = iota
	running
	failed
	finished
)

const (
	ansiCursorUpFmt      = "\033[%dA"
	ansiClearToScreenEnd = "\033[J"
)

type Tester struct {
	name          string
	status        status
	cpuFrequency  int64
	testForTsc    int64
	expectedBytes int64

	best           *result
	current        *result
	bestFoundAtTsc int64

	err error

	results     []*result
	tscSum      int64
	bytesSum    int64
	minResult   *result
	maxResult   *result
	outputLines int
}

type result struct {
	tscStart int64

	tscElapsed     int64
	processedBytes int64
}

func newResult() *result {
	return &result{}
}

func NewTester(name string) *Tester {
	cpuFrequency := getCPUTimerFreq(1000)
	return &Tester{
		name:           name,
		status:         starting,
		cpuFrequency:   cpuFrequency,
		testForTsc:     cpuFrequency * 10,
		bestFoundAtTsc: readCPUTimer(),
		results:        make([]*result, 0),
	}
}

func (t *Tester) IsRunning() bool {
	if t.status < failed {
		return readCPUTimer()-t.bestFoundAtTsc < t.testForTsc
	}

	return false
}

func (t *Tester) WithExpectedBytes(expected int64) *Tester {
	t.expectedBytes = expected
	return t
}

func (t *Tester) Begin() error {
	t.output()

	switch t.status {
	case starting, running:
		result := newResult()
		result.tscStart = readCPUTimer()
		t.current = result
		t.status = running
	case finished:
		// Repetition test finished, can't begin again
		return fmt.Errorf("tester finished")
	case failed:
		return fmt.Errorf("tester failed with error: %w", t.err)
	}

	return nil
}

func (t *Tester) End() error {
	if t.status != running {
		return t.failf("tester must be running before calling End()")
	}

	tscEnd := readCPUTimer()
	t.current.tscElapsed = tscEnd - t.current.tscStart

	if t.best == nil || t.best.tscElapsed > t.current.tscElapsed {
		t.bestFoundAtTsc = tscEnd
		t.best = t.current
	}

	t.tscSum += t.current.tscElapsed
	if t.minResult == nil || t.current.tscElapsed < t.minResult.tscElapsed {
		t.minResult = t.current
	}
	if t.maxResult == nil || t.current.tscElapsed > t.maxResult.tscElapsed {
		t.maxResult = t.current
	}

	idx, _ := slices.BinarySearchFunc(t.results, t.current, func(a, b *result) int {
		return int(a.tscElapsed - b.tscElapsed)
	})
	t.results = slices.Insert(t.results, idx, t.current)

	return nil
}

func (t *Tester) Fail() {
	t.status = failed
}

func (t *Tester) Err() error {
	return t.err
}

func (t *Tester) failf(format string, args ...any) error {
	t.status = failed
	t.err = fmt.Errorf(format, args...)
	return t.err
}

func (t *Tester) CountBytes(count int) error {
	if t.expectedBytes != int64(count) {
		return t.failf("expected %d bytes, processed %d", t.expectedBytes, count)
	}

	t.current.processedBytes = int64(count)
	t.bytesSum += int64(count)
	return nil
}

func readOSTimer() int64 {
	return time.Now().UnixMicro()
}

func getOSTimerFreq() int64 {
	return 1000000
}

func readCPUTimer() int64 {
	cvalue := C.ReadCPUTimer()
	return int64(cvalue)
}

// NOTE: Might want to move this into a commons package
func getCPUTimerFreq(millisecondsToWait int64) int64 {
	osFrequency := getOSTimerFreq()

	cpuStart := readCPUTimer()
	osStart := readOSTimer()
	var osEnd, osElapsed int64
	osWaitTime := osFrequency * millisecondsToWait / 1000
	for osElapsed < osWaitTime {
		osEnd = readOSTimer()
		osElapsed = osEnd - osStart
	}

	cpuEnd := readCPUTimer()
	cpuElapsed := cpuEnd - cpuStart
	cpuFrequency := osFrequency * cpuElapsed / osElapsed

	return cpuFrequency
}

func (t *Tester) msFromTsc(tsc int64) float64 {
	return 1000 * float64(tsc) / float64(t.cpuFrequency)
}

func throughputGBs(processedBytes int64, ms float64) float64 {
	if ms == 0 {
		return 0
	}
	gigabytes := float64(processedBytes) / (1024 * 1024 * 1024)
	return gigabytes / (ms / 1000)
}

func (t *Tester) output() {
	if t.current != nil {
		if t.outputLines > 0 {
			fmt.Printf(ansiCursorUpFmt+ansiClearToScreenEnd, t.outputLines)
		}

		count := len(t.results)
		median := t.results[count/2]

		meanMs := t.msFromTsc(t.tscSum / int64(count))
		meanBytes := t.bytesSum / int64(count)

		minMs := t.msFromTsc(t.minResult.tscElapsed)
		maxMs := t.msFromTsc(t.maxResult.tscElapsed)
		medianMs := t.msFromTsc(median.tscElapsed)

		fmt.Printf("%s\n", t.name)
		fmt.Printf("  Min:    %10.3fms, %7.3fGB/s\n", minMs, throughputGBs(t.minResult.processedBytes, minMs))
		fmt.Printf("  Max:    %10.3fms, %7.3fGB/s\n", maxMs, throughputGBs(t.maxResult.processedBytes, maxMs))
		fmt.Printf("  Mean:   %10.3fms, %7.3fGB/s\n", meanMs, throughputGBs(meanBytes, meanMs))
		fmt.Printf("  Median: %10.3fms, %7.3fGB/s\n", medianMs, throughputGBs(median.processedBytes, medianMs))

		t.outputLines = 5
	}
}
