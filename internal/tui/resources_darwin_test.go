//go:build darwin

package tui

import "testing"

func TestParsePsTimeToTicks_ShouldParseMinutesSecondsCentis(t *testing.T) {
	// 9:10.43 == 9*60 + 10.43 == 550.43 seconds == 55043 centiseconds
	got := parsePsTimeToTicks("9:10.43")
	want := int64(55043)
	if got != want {
		t.Errorf("parsePsTimeToTicks(\"9:10.43\") = %d, want %d", got, want)
	}
}

func TestParsePsTimeToTicks_ShouldParseLargeMinutes(t *testing.T) {
	// BSD ps shows the minutes column unbounded, e.g. 802:27.81 for ~13.4h.
	// 802*60 + 27.81 == 48147.81 seconds == 4814781 centiseconds
	got := parsePsTimeToTicks("802:27.81")
	want := int64(4814781)
	if got != want {
		t.Errorf("parsePsTimeToTicks(\"802:27.81\") = %d, want %d", got, want)
	}
}

func TestParsePsTimeToTicks_ShouldParseHoursMinutesSeconds(t *testing.T) {
	// 1:02:03.04 == 1*3600 + 2*60 + 3.04 == 3723.04 seconds == 372304 centis
	got := parsePsTimeToTicks("1:02:03.04")
	want := int64(372304)
	if got != want {
		t.Errorf("parsePsTimeToTicks(\"1:02:03.04\") = %d, want %d", got, want)
	}
}

func TestParsePsTimeToTicks_ShouldParseDaysHoursMinutesSeconds(t *testing.T) {
	// 2-03:04:05.06 == 2*86400 + 3*3600 + 4*60 + 5.06 seconds
	// == 172800 + 10800 + 240 + 5.06 == 183845.06 seconds == 18384506 centis
	got := parsePsTimeToTicks("2-03:04:05.06")
	want := int64(18384506)
	if got != want {
		t.Errorf("parsePsTimeToTicks(\"2-03:04:05.06\") = %d, want %d", got, want)
	}
}

func TestParsePsTimeToTicks_ShouldReturnZero_GivenGarbage(t *testing.T) {
	got := parsePsTimeToTicks("not-a-time")
	if got != 0 {
		t.Errorf("parsePsTimeToTicks(\"not-a-time\") = %d, want 0", got)
	}
}

func TestListAllProcesses_ShouldReturnNonEmpty(t *testing.T) {
	procs := listAllProcesses()
	if len(procs) == 0 {
		t.Fatal("listAllProcesses() returned no processes; expected at least the test runner")
	}
	// Sanity-check at least one entry has a non-zero pid.
	var sawPID bool
	for pid := range procs {
		if pid > 0 {
			sawPID = true
			break
		}
	}
	if !sawPID {
		t.Error("listAllProcesses() returned no entries with pid > 0")
	}
}

func TestReadAllProcTicks_ShouldReturnNonEmpty(t *testing.T) {
	ticks := readAllProcTicks()
	if len(ticks) == 0 {
		t.Fatal("readAllProcTicks() returned empty map; expected ticks for at least one process")
	}
}

func TestGetTotalMemoryKB_ShouldReturnPositive(t *testing.T) {
	got := getTotalMemoryKB()
	if got <= 0 {
		t.Errorf("getTotalMemoryKB() = %d, want > 0", got)
	}
}

func TestGetSystemMemPercent_ShouldReturnInRange(t *testing.T) {
	got := getSystemMemPercent()
	if got < 0 || got > 100 {
		t.Errorf("getSystemMemPercent() = %f, want between 0 and 100", got)
	}
	// On any running test machine memory usage will be well above zero.
	if got == 0 {
		t.Error("getSystemMemPercent() = 0; expected a positive percentage on a running system")
	}
}
