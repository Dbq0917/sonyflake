package sonyflake

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/sony/sonyflake/mock"
	"github.com/sony/sonyflake/types"
)

var sf *Sonyflake

var startTime int64
var machineID uint64

func init() {
	var st Settings
	st.StartTime = time.Now()

	sf = NewSonyflake(st)
	if sf == nil {
		panic("sonyflake not created")
	}

	startTime = toSonyflakeTime(st.StartTime)

	ip, _ := lower16BitPrivateIP(defaultInterfaceAddrs)
	machineID = uint64(ip)
}

func nextID(t *testing.T) uint64 {
	id, err := sf.NextID()
	if err != nil {
		t.Fatal("id not generated")
	}
	return id
}

func TestNew(t *testing.T) {
	genError := fmt.Errorf("an error occurred while generating ID")

	tests := []struct {
		name     string
		settings Settings
		err      error
	}{
		{
			name: "failure: time ahead",
			settings: Settings{
				StartTime: time.Now().Add(time.Minute),
			},
			err: ErrStartTimeAhead,
		},
		{
			name: "failure: machine ID",
			settings: Settings{
				MachineID: func() (uint16, error) {
					return 0, genError
				},
			},
			err: genError,
		},
		{
			name: "failure: invalid machine ID",
			settings: Settings{
				CheckMachineID: func(uint16) bool {
					return false
				},
			},
			err: ErrInvalidMachineID,
		},
		{
			name:     "success",
			settings: Settings{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sonyflake, err := New(test.settings)

			if !errors.Is(err, test.err) {
				t.Fatalf("unexpected value, want %#v, got %#v", test.err, err)
			}

			if sonyflake == nil && err == nil {
				t.Fatal("unexpected value, sonyflake should not be nil")
			}
		})
	}
}

func TestSonyflakeOnce(t *testing.T) {
	sleepTime := time.Duration(50 * sonyflakeTimeUnit)
	time.Sleep(sleepTime)

	id := nextID(t)

	actualTime := ElapsedTime(id)
	if actualTime < sleepTime || actualTime > sleepTime+sonyflakeTimeUnit {
		t.Errorf("unexpected time: %d", actualTime)
	}

	actualSequence := SequenceNumber(id)
	if actualSequence != 0 {
		t.Errorf("unexpected sequence: %d", actualSequence)
	}

	actualMachineID := MachineID(id)
	if actualMachineID != machineID {
		t.Errorf("unexpected machine id: %d", actualMachineID)
	}

	fmt.Println("sonyflake id:", id)
	fmt.Println("decompose:", Decompose(id))
}

func currentTime() int64 {
	return toSonyflakeTime(time.Now())
}

func TestSonyflakeFor10Sec(t *testing.T) {
	var numID uint32
	var lastID uint64
	var maxSequence uint64

	initial := currentTime()
	current := initial
	for current-initial < 1000 {
		id := nextID(t)
		parts := Decompose(id)
		numID++

		if id == lastID {
			t.Fatal("duplicated id")
		}
		if id < lastID {
			t.Fatal("must increase with time")
		}
		lastID = id

		current = currentTime()

		actualMSB := parts["msb"]
		if actualMSB != 0 {
			t.Errorf("unexpected msb: %d", actualMSB)
		}

		actualTime := int64(parts["time"])
		overtime := startTime + actualTime - current
		if overtime > 0 {
			t.Errorf("unexpected overtime: %d", overtime)
		}

		actualSequence := parts["sequence"]
		if maxSequence < actualSequence {
			maxSequence = actualSequence
		}

		actualMachineID := parts["machine-id"]
		if actualMachineID != machineID {
			t.Errorf("unexpected machine id: %d", actualMachineID)
		}
	}

	if maxSequence != 1<<BitLenSequence-1 {
		t.Errorf("unexpected max sequence: %d", maxSequence)
	}
	fmt.Println("max sequence:", maxSequence)
	fmt.Println("number of id:", numID)
}

func TestSonyflakeInParallel(t *testing.T) {
	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)
	fmt.Println("number of cpu:", numCPU)

	consumer := make(chan uint64)

	const numID = 10000
	generate := func() {
		for i := 0; i < numID; i++ {
			consumer <- nextID(t)
		}
	}

	const numGenerator = 10
	for i := 0; i < numGenerator; i++ {
		go generate()
	}

	set := make(map[uint64]struct{})
	for i := 0; i < numID*numGenerator; i++ {
		id := <-consumer
		if _, ok := set[id]; ok {
			t.Fatal("duplicated id")
		}
		set[id] = struct{}{}
	}
	fmt.Println("number of id:", len(set))
}

func newBatchAllocationTestSonyflake(t *testing.T, machineID uint16, start time.Time) *Sonyflake {
	t.Helper()
	sonyflake, err := New(Settings{
		StartTime: start,
		MachineID: func() (uint16, error) {
			return machineID, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return sonyflake
}

func TestNextIDBatchAllocation(t *testing.T) {
	const size = 1000
	const machineID = 42

	sf := newBatchAllocationTestSonyflake(t, machineID, time.Now().Add(-time.Second))
	ids, err := sf.NextIDBatch(size)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != size {
		t.Fatalf("unexpected batch size: got %d, want %d", len(ids), size)
	}

	seen := make(map[uint64]struct{}, size)
	expectedTime := int64(ElapsedTime(ids[0]) / sonyflakeTimeUnit)
	expectedSequence := uint64(0)
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicated id: %d", id)
		}
		seen[id] = struct{}{}

		if MachineID(id) != machineID {
			t.Errorf("unexpected machine id: %d", MachineID(id))
		}
		if int64(ElapsedTime(id)/sonyflakeTimeUnit) != expectedTime {
			t.Errorf("unexpected time part: %d, want %d", ElapsedTime(id)/sonyflakeTimeUnit, expectedTime)
		}
		if SequenceNumber(id) != expectedSequence {
			t.Errorf("unexpected sequence: %d, want %d", SequenceNumber(id), expectedSequence)
		}

		expectedSequence++
		if expectedSequence == 1<<BitLenSequence {
			expectedSequence = 0
			expectedTime++
		}
	}

	next, err := sf.NextID()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := seen[next]; ok {
		t.Fatalf("NextID collided with batch: %d", next)
	}
	if SequenceNumber(next) != expectedSequence || int64(ElapsedTime(next)/sonyflakeTimeUnit) != expectedTime {
		t.Fatalf("NextID did not continue after batch: got time=%d sequence=%d, want time=%d sequence=%d",
			ElapsedTime(next)/sonyflakeTimeUnit, SequenceNumber(next), expectedTime, expectedSequence)
	}

	aliasIDs, err := sf.NextIDs(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliasIDs) != 2 || aliasIDs[0] == aliasIDs[1] {
		t.Fatalf("unexpected NextIDs result: %v", aliasIDs)
	}
}

func TestNextIDBatchAllocationInvalidSize(t *testing.T) {
	sf := newBatchAllocationTestSonyflake(t, 1, time.Now().Add(-time.Second))

	ids, err := sf.NextIDBatch(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("unexpected ids: %v", ids)
	}

	if _, err := sf.NextIDBatch(-1); !errors.Is(err, ErrInvalidBatchSize) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNextIDBatchAllocationOverTimeLimit(t *testing.T) {
	sf := newBatchAllocationTestSonyflake(t, 1, time.Now().Add(-time.Second))
	sf.elapsedTime = 1<<BitLenTime - 1
	sf.sequence = 1<<BitLenSequence - 1

	elapsedTime, sequence := sf.elapsedTime, sf.sequence
	if _, err := sf.NextIDBatch(1); !errors.Is(err, ErrOverTimeLimit) {
		t.Fatalf("unexpected error: %v", err)
	}
	if sf.elapsedTime != elapsedTime || sf.sequence != sequence {
		t.Fatal("failed batch consumed sequence state")
	}
}

func TestNextIDBatchAllocationAcrossInstances(t *testing.T) {
	start := time.Now().Add(-time.Second)
	sf1 := newBatchAllocationTestSonyflake(t, 1, start)
	sf2 := newBatchAllocationTestSonyflake(t, 2, start)

	ids1, err := sf1.NextIDBatch(5000)
	if err != nil {
		t.Fatal(err)
	}
	ids2, err := sf2.NextIDBatch(5000)
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[uint64]struct{}, len(ids1)+len(ids2))
	for _, ids := range [][]uint64{ids1, ids2} {
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				t.Fatalf("duplicated id across instances: %d", id)
			}
			seen[id] = struct{}{}
		}
	}
}

func TestNextIDBatchAllocationInParallel(t *testing.T) {
	const (
		numWorkers = 100
		batchSize  = 1000
	)

	sf := newBatchAllocationTestSonyflake(t, 1, time.Now().Add(-time.Second))
	results := make(chan []uint64, numWorkers)
	errors := make(chan error, numWorkers)

	start := time.Now()
	for i := 0; i < numWorkers; i++ {
		go func() {
			ids, err := sf.NextIDBatch(batchSize)
			if err != nil {
				errors <- err
				return
			}
			results <- ids
		}()
	}

	seen := make(map[uint64]struct{}, numWorkers*batchSize)
	for i := 0; i < numWorkers; i++ {
		select {
		case err := <-errors:
			t.Fatal(err)
		case ids := <-results:
			for _, id := range ids {
				if _, ok := seen[id]; ok {
					t.Fatalf("duplicated id: %d", id)
				}
				seen[id] = struct{}{}
			}
		}
	}
	elapsed := time.Since(start)

	if len(seen) != numWorkers*batchSize {
		t.Fatalf("unexpected number of ids: got %d, want %d", len(seen), numWorkers*batchSize)
	}
	t.Logf("generated %d ids in %v (%.0f ids/s)", len(seen), elapsed, float64(len(seen))/elapsed.Seconds())
}

func pseudoSleep(period time.Duration) {
	sf.startTime -= int64(period) / sonyflakeTimeUnit
}

func TestNextIDError(t *testing.T) {
	year := time.Duration(365*24) * time.Hour
	pseudoSleep(time.Duration(174) * year)
	nextID(t)

	pseudoSleep(time.Duration(1) * year)
	_, err := sf.NextID()
	if err == nil {
		t.Errorf("time is not over")
	}
}

func TestPrivateIPv4(t *testing.T) {
	testCases := []struct {
		description    string
		expected       net.IP
		interfaceAddrs types.InterfaceAddrs
		error          string
	}{
		{
			description:    "InterfaceAddrs returns an error",
			expected:       nil,
			interfaceAddrs: mock.NewFailingInterfaceAddrs(),
			error:          "test error",
		},
		{
			description:    "InterfaceAddrs returns an empty or nil list",
			expected:       nil,
			interfaceAddrs: mock.NewNilInterfaceAddrs(),
			error:          "no private ip address",
		},
		{
			description:    "InterfaceAddrs returns one or more IPs",
			expected:       net.IP{192, 168, 0, 1},
			interfaceAddrs: mock.NewSuccessfulInterfaceAddrs(),
			error:          "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := privateIPv4(tc.interfaceAddrs)

			if (err != nil) && (tc.error == "") {
				t.Errorf("expected no error, but got: %s", err)
				return
			} else if (err != nil) && (tc.error != "") {
				return
			}

			if net.IP.Equal(actual, tc.expected) {
				return
			} else {
				t.Errorf("error: expected: %s, but got: %s", tc.expected, actual)
			}
		})
	}
}

func TestLower16BitPrivateIP(t *testing.T) {
	testCases := []struct {
		description    string
		expected       uint16
		interfaceAddrs types.InterfaceAddrs
		error          string
	}{
		{
			description:    "InterfaceAddrs returns an empty or nil list",
			expected:       0,
			interfaceAddrs: mock.NewNilInterfaceAddrs(),
			error:          "no private ip address",
		},
		{
			description:    "InterfaceAddrs returns one or more IPs",
			expected:       1,
			interfaceAddrs: mock.NewSuccessfulInterfaceAddrs(),
			error:          "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := lower16BitPrivateIP(tc.interfaceAddrs)

			if (err != nil) && (tc.error == "") {
				t.Errorf("expected no error, but got: %s", err)
				return
			} else if (err != nil) && (tc.error != "") {
				return
			}

			if actual == tc.expected {
				return
			} else {
				t.Errorf("error: expected: %v, but got: %v", tc.expected, actual)
			}
		})
	}
}

func TestSonyflakeTimeUnit(t *testing.T) {
	if time.Duration(sonyflakeTimeUnit) != 10*time.Millisecond {
		t.Errorf("unexpected time unit")
	}
}

func TestCompose(t *testing.T) {
	var st Settings
	st.StartTime = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	sf, err := New(st)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	sequence := uint16(123)
	machineID := uint16(456)

	id, err := Compose(sf, now, sequence, machineID)
	if err != nil {
		t.Fatal(err)
	}

	parts := Decompose(id)

	actualTime := toSonyflakeTime(now) - toSonyflakeTime(st.StartTime)
	if parts["time"] != uint64(actualTime) {
		t.Errorf("unexpected time: %d", parts["time"])
	}

	if parts["sequence"] != uint64(sequence) {
		t.Errorf("unexpected sequence: %d", parts["sequence"])
	}

	if parts["machine-id"] != uint64(machineID) {
		t.Errorf("unexpected machine id: %d", parts["machine-id"])
	}
}
