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

// vmStatAppleSiliconSample is real `vm_stat` output captured on an M-series
// Mac with 64 GiB RAM (page size 16384). Used to lock in the Activity
// Monitor-style "Memory Used" formula so it doesn't regress back to the
// older (active + wired + compressed) calculation that overcounted by tens
// of percentage points on Apple Silicon.
const vmStatAppleSiliconSample = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                              218300.
Pages active:                           1710528.
Pages inactive:                         1690555.
Pages speculative:                        19287.
Pages throttled:                              0.
Pages wired down:                        289969.
Pages purgeable:                          33053.
"Translation faults":                2606666516.
Pages copy-on-write:                  212606154.
Pages zero filled:                   1609269626.
Pages reactivated:                      3966618.
Pages purged:                           4595027.
File-backed pages:                      1028761.
Anonymous pages:                        2372980.
Pages stored in compressor:              669728.
Pages occupied by compressor:            224419.
Decompressions:                          983255.
Compressions:                           1383735.
Pageins:                               11200986.
Pageouts:                                203837.
Swapins:                                      0.
Swapouts:                                     0.
`

// vmStatIntelSample mirrors the same workload at the Intel 4 KiB page size so
// we exercise the dynamic page-size parsing branch.
const vmStatIntelSample = `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                              218300.
Pages active:                           1710528.
Pages inactive:                         1690555.
Pages speculative:                        19287.
Pages throttled:                              0.
Pages wired down:                        289969.
Pages purgeable:                          33053.
File-backed pages:                      1028761.
Anonymous pages:                        2372980.
Pages occupied by compressor:            224419.
`

func TestParseVmStatUsedBytes_AppleSiliconMatchesActivityMonitorFormula(t *testing.T) {
	got, ok := parseVmStatUsedBytes(vmStatAppleSiliconSample)
	if !ok {
		t.Fatal("parseVmStatUsedBytes returned ok=false on valid input")
	}
	// (anonymous - purgeable) + wired + compressed, scaled by 16 KiB page:
	// (2372980 - 33053) + 289969 + 224419 = 2854315 pages * 16384 = 46765096960
	const want int64 = 46765096960
	if got != want {
		t.Errorf("parseVmStatUsedBytes = %d, want %d", got, want)
	}
}

func TestParseVmStatUsedBytes_IntelUsesParsedPageSize(t *testing.T) {
	got, ok := parseVmStatUsedBytes(vmStatIntelSample)
	if !ok {
		t.Fatal("parseVmStatUsedBytes returned ok=false on valid input")
	}
	// Same page counts, but 4 KiB page: 2854315 * 4096 = 11691274240
	const want int64 = 11691274240
	if got != want {
		t.Errorf("parseVmStatUsedBytes = %d, want %d", got, want)
	}
}

func TestParseVmStatUsedBytes_IgnoresFileBackedAndActiveFields(t *testing.T) {
	// Construct a sample where "Pages active" and "File-backed pages" are
	// huge but anonymous/wired/compressed are tiny. The old
	// (active + wired + compressed) formula would have produced a large
	// number; the new formula should ignore those reclaimable file caches
	// entirely and report a small value driven only by anonymous + wired +
	// compressed.
	input := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages active:                          1000000.
Pages inactive:                              0.
Pages wired down:                          100.
Pages purgeable:                             0.
File-backed pages:                      999000.
Anonymous pages:                          1000.
Pages occupied by compressor:               50.
`
	got, ok := parseVmStatUsedBytes(input)
	if !ok {
		t.Fatal("parseVmStatUsedBytes returned ok=false on valid input")
	}
	// (1000 - 0) + 100 + 50 = 1150 pages * 4096 = 4710400 bytes.
	// Critically this is ~1000x smaller than what the previous formula
	// would have produced (which would have summed in the 1,000,000-page
	// active set, ~99% of which is file cache).
	const want int64 = 4710400
	if got != want {
		t.Errorf("parseVmStatUsedBytes = %d, want %d", got, want)
	}
}

func TestParseVmStatUsedBytes_ReturnsFalseOnMissingFields(t *testing.T) {
	// Only the page-size header; no usable counts.
	_, ok := parseVmStatUsedBytes("Mach Virtual Memory Statistics: (page size of 16384 bytes)\n")
	if ok {
		t.Error("parseVmStatUsedBytes returned ok=true on header-only input")
	}
}

func TestParseVmStatUsedBytes_ClampsNegativeAppPagesToZero(t *testing.T) {
	// Pathological: purgeable > anonymous. Real systems don't hit this, but
	// the sysctls are independently sampled so a brief inversion is possible.
	// We must not produce a negative byte count.
	input := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Anonymous pages:                              10.
Pages purgeable:                              50.
Pages wired down:                              5.
Pages occupied by compressor:                  3.
`
	got, ok := parseVmStatUsedBytes(input)
	if !ok {
		t.Fatal("parseVmStatUsedBytes returned ok=false on valid input")
	}
	// appPages clamped to 0, so result is (0 + 5 + 3) * 4096 = 32768.
	const want int64 = 32768
	if got != want {
		t.Errorf("parseVmStatUsedBytes = %d, want %d", got, want)
	}
}
