/* Package tester can be used to perform repetition tests */
package tester

import (
	"fmt"
	"slices"
	"syscall"

	"github.com/fcassin/gotimer/timer"
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

	results      []*result
	tscSum       int64
	bytesSum     int64
	minFaultsSum int64
	majFaultsSum int64
	minResult    *result
	maxResult    *result
	outputLines  int
}

type result struct {
	tscStart      int64
	minFaultStart int64
	majFaultStart int64

	tscElapsed     int64
	processedBytes int64
	minFaults      int64
	majFaults      int64
}

func newResult() *result {
	return &result{}
}

func NewTester(name string) *Tester {
	cpuFrequency := timer.GetCPUTimerFreq(1000)
	return &Tester{
		name:           name,
		status:         starting,
		cpuFrequency:   cpuFrequency,
		testForTsc:     cpuFrequency * 10,
		bestFoundAtTsc: timer.ReadCPUTimer(),
		results:        make([]*result, 0),
	}
}

func (t *Tester) IsRunning() bool {
	if t.status < failed {
		return timer.ReadCPUTimer()-t.bestFoundAtTsc < t.testForTsc
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
		var ru syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
			return t.failf("getrusage: %w", err)
		}

		result := newResult()
		result.tscStart = timer.ReadCPUTimer()
		result.minFaultStart = ru.Minflt
		result.majFaultStart = ru.Majflt
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

	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return t.failf("getrusage: %w", err)
	}

	tscEnd := timer.ReadCPUTimer()
	t.current.tscElapsed = tscEnd - t.current.tscStart
	t.current.minFaults = ru.Minflt - t.current.minFaultStart
	t.current.majFaults = ru.Majflt - t.current.majFaultStart

	if t.best == nil || t.best.tscElapsed > t.current.tscElapsed {
		t.bestFoundAtTsc = tscEnd
		t.best = t.current
	}

	t.tscSum += t.current.tscElapsed
	t.minFaultsSum += t.current.minFaults
	t.majFaultsSum += t.current.majFaults
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

func kbPerFault(processedBytes, faults int64) float64 {
	if faults == 0 {
		return 0
	}
	kilobytes := float64(processedBytes) / 1024
	return kilobytes / float64(faults)
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
		meanFaults := (t.minFaultsSum + t.majFaultsSum) / int64(count)

		minMs := t.msFromTsc(t.minResult.tscElapsed)
		maxMs := t.msFromTsc(t.maxResult.tscElapsed)
		medianMs := t.msFromTsc(median.tscElapsed)

		minFaults := t.minResult.minFaults + t.minResult.majFaults
		maxFaults := t.maxResult.minFaults + t.maxResult.majFaults
		medianFaults := median.minFaults + median.majFaults

		fmt.Printf("%s\n", t.name)
		fmt.Printf("  Min:    %10.3fms, %7.3fGB/s, PF: %6d (%8.1f kB/fault)\n",
			minMs, throughputGBs(t.minResult.processedBytes, minMs), minFaults, kbPerFault(t.minResult.processedBytes, minFaults))
		fmt.Printf("  Max:    %10.3fms, %7.3fGB/s, PF: %6d (%8.1f kB/fault)\n",
			maxMs, throughputGBs(t.maxResult.processedBytes, maxMs), maxFaults, kbPerFault(t.maxResult.processedBytes, maxFaults))
		fmt.Printf("  Mean:   %10.3fms, %7.3fGB/s, PF: %6d (%8.1f kB/fault)\n",
			meanMs, throughputGBs(meanBytes, meanMs), meanFaults, kbPerFault(meanBytes, meanFaults))
		fmt.Printf("  Median: %10.3fms, %7.3fGB/s, PF: %6d (%8.1f kB/fault)\n",
			medianMs, throughputGBs(median.processedBytes, medianMs), medianFaults, kbPerFault(median.processedBytes, medianFaults))

		t.outputLines = 5
	}
}
