package sonyflake

import (
	"errors"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sony/sonyflake/v2/mock"
	"github.com/sony/sonyflake/v2/types"
)

func TestNew(t *testing.T) {
	errGetMachineID := errors.New("failed to get machine id")

	testCases := []struct {
		name     string
		settings Settings
		err      error
	}{
		{
			name: "invalid bit length for time",
			settings: Settings{
				BitsSequence:  16,
				BitsMachineID: 16,
			},
			err: ErrInvalidBitsTime,
		},
		{
			name: "invalid bit length for sequence number",
			settings: Settings{
				BitsSequence: -1,
			},
			err: ErrInvalidBitsSequence,
		},
		{
			name: "invalid bit length for machine id",
			settings: Settings{
				BitsMachineID: 31,
			},
			err: ErrInvalidBitsMachineID,
		},
		{
			name: "invalid time unit",
			settings: Settings{
				TimeUnit: time.Microsecond,
			},
			err: ErrInvalidTimeUnit,
		},
		{
			name: "start time ahead",
			settings: Settings{
				StartTime: time.Now().Add(time.Minute),
			},
			err: ErrStartTimeAhead,
		},
		{
			name: "cannot get machine id",
			settings: Settings{
				MachineID: func() (int, error) {
					return 0, errGetMachineID
				},
			},
			err: errGetMachineID,
		},
		{
			name: "too large machine id",
			settings: Settings{
				MachineID: func() (int, error) {
					return 1 << defaultBitsMachine, nil
				},
			},
			err: ErrInvalidMachineID,
		},
		{
			name: "negative machine id",
			settings: Settings{
				MachineID: func() (int, error) {
					return -1, nil
				},
			},
			err: ErrInvalidMachineID,
		},
		{
			name: "invalid machine id",
			settings: Settings{
				CheckMachineID: func(int) bool {
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

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sf, err := New(tc.settings)

			if !errors.Is(err, tc.err) {
				t.Fatalf("unexpected error: %v", err)
			}

			if err == nil && sf == nil {
				t.Fatal("sonyflake instance must be created")
			}
		})
	}
}

func newSonyflake(t *testing.T, st Settings) *Sonyflake {
	sf, err := New(st)
	if err != nil {
		t.Fatalf("failed to create sonyflake: %v", err)
	}
	return sf
}

func nextID(t *testing.T, sf *Sonyflake) int64 {
	id, err := sf.NextID()
	if err != nil {
		t.Fatalf("failed to generate id: %v", err)
	}
	return id
}

func defaultMachineID(t *testing.T) int {
	ip, err := lower16BitPrivateIP(defaultInterfaceAddrs)
	if err != nil {
		t.Fatalf("failed to get private ip address: %v", err)
	}
	return ip
}

func TestNextID(t *testing.T) {
	start := time.Now()
	sf := newSonyflake(t, Settings{StartTime: start})

	sleepTime := int64(50)
	sf.now = func() time.Time { return start.Add(time.Duration(sleepTime * sf.timeUnit)) }

	id := nextID(t, sf)

	actualTime := sf.timePart(id)
	if actualTime != sleepTime {
		t.Errorf("unexpected time: %d", actualTime)
	}

	actualSequence := sf.sequencePart(id)
	if actualSequence != 0 {
		t.Errorf("unexpected sequence: %d", actualSequence)
	}

	actualMachine := sf.machinePart(id)
	if actualMachine != int64(defaultMachineID(t)) {
		t.Errorf("unexpected machine: %d", actualMachine)
	}

	t.Log("sonyflake id:", id)
	t.Log("decompose:", sf.Decompose(id))
}

func TestNextID_InSequence(t *testing.T) {
	start := time.Now()
	sf := newSonyflake(t, Settings{
		TimeUnit:  time.Millisecond,
		StartTime: start,
	})
	startTime := sf.toInternalTime(start)
	machineID := int64(defaultMachineID(t))

	var numID int
	var lastID int64
	var maxSeq int64

	currentTime := startTime
	for currentTime-startTime < 100 {
		id := nextID(t, sf)
		currentTime = sf.toInternalTime(time.Now())
		numID++

		if id == lastID {
			t.Fatal("duplicated id")
		}
		if id < lastID {
			t.Fatal("must increase with time")
		}
		lastID = id

		parts := sf.Decompose(id)

		actualTime := parts["time"]
		overtime := startTime + actualTime - currentTime
		if overtime > 0 {
			t.Errorf("unexpected overtime: %d", overtime)
		}

		actualSequence := parts["sequence"]
		if actualSequence > maxSeq {
			maxSeq = actualSequence
		}

		actualMachine := parts["machine"]
		if actualMachine != machineID {
			t.Errorf("unexpected machine: %d", actualMachine)
		}
	}

	if maxSeq > 1<<sf.bitsSequence-1 {
		t.Errorf("unexpected max sequence: %d", maxSeq)
	}
	t.Log("max sequence:", maxSeq)
	t.Log("number of id:", numID)
}

func TestNextID_InParallel(t *testing.T) {
	sf1 := newSonyflake(t, Settings{MachineID: func() (int, error) { return 1, nil }})
	sf2 := newSonyflake(t, Settings{MachineID: func() (int, error) { return 2, nil }})

	numCPU := runtime.NumCPU()
	runtime.GOMAXPROCS(numCPU)
	t.Log("number of cpu:", numCPU)

	consumer := make(chan int64)

	const numID = 1000
	generate := func(sf *Sonyflake) {
		for i := 0; i < numID; i++ {
			id := nextID(t, sf)
			consumer <- id
		}
	}

	var numGenerator int
	for i := 0; i < numCPU/2; i++ {
		go generate(sf1)
		go generate(sf2)
		numGenerator += 2
	}

	set := make(map[int64]struct{})
	for i := 0; i < numID*numGenerator; i++ {
		id := <-consumer
		if _, ok := set[id]; ok {
			t.Fatal("duplicated id")
		}
		set[id] = struct{}{}
	}
	t.Log("number of id:", len(set))
}

func newBatchAllocationTestSonyflake(t *testing.T, machineID int, start time.Time) *Sonyflake {
	t.Helper()
	return newSonyflake(t, Settings{
		StartTime: start,
		MachineID: func() (int, error) {
			return machineID, nil
		},
	})
}

func TestNextIDBatchAllocation(t *testing.T) {
	const size = 1000
	const machineID = 42

	start := time.Now().Add(-time.Second)
	sf := newBatchAllocationTestSonyflake(t, machineID, start)
	var nowCalls int32
	sf.now = func() time.Time {
		atomic.AddInt32(&nowCalls, 1)
		return start
	}

	ids, err := sf.NextIDBatch(size)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != size {
		t.Fatalf("unexpected batch size: got %d, want %d", len(ids), size)
	}
	if got := atomic.LoadInt32(&nowCalls); got != 1 {
		t.Fatalf("unexpected clock reads: got %d, want 1", got)
	}

	seen := make(map[int64]struct{}, size)
	expectedTime := sf.timePart(ids[0])
	expectedSequence := int64(0)
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicated id: %d", id)
		}
		seen[id] = struct{}{}

		if sf.machinePart(id) != machineID {
			t.Errorf("unexpected machine id: %d", sf.machinePart(id))
		}
		if sf.timePart(id) != expectedTime {
			t.Errorf("unexpected time part: %d, want %d", sf.timePart(id), expectedTime)
		}
		if sf.sequencePart(id) != expectedSequence {
			t.Errorf("unexpected sequence: %d, want %d", sf.sequencePart(id), expectedSequence)
		}

		expectedSequence++
		if expectedSequence == 1<<sf.bitsSequence {
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
	if sf.sequencePart(next) != expectedSequence || sf.timePart(next) != expectedTime {
		t.Fatalf("NextID did not continue after batch: got time=%d sequence=%d, want time=%d sequence=%d",
			sf.timePart(next), sf.sequencePart(next), expectedTime, expectedSequence)
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
	sf.elapsedTime = 1<<sf.bitsTime - 1
	sf.sequence = 1<<sf.bitsSequence - 1

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

	seen := make(map[int64]struct{}, len(ids1)+len(ids2))
	for _, ids := range [][]int64{ids1, ids2} {
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
	results := make(chan []int64, numWorkers)
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

	seen := make(map[int64]struct{}, numWorkers*batchSize)
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

func pseudoSleep(sf *Sonyflake, period time.Duration) {
	sf.startTime -= int64(period) / sf.timeUnit
}

const year = time.Duration(365*24) * time.Hour

func TestNextID_ReturnsError(t *testing.T) {
	sf := newSonyflake(t, Settings{StartTime: time.Now()})

	pseudoSleep(sf, time.Duration(174)*year)
	nextID(t, sf)

	pseudoSleep(sf, time.Duration(1)*year)
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
		error          error
	}{
		{
			description:    "returns an error",
			expected:       nil,
			interfaceAddrs: mock.NewFailingInterfaceAddrs(),
			error:          mock.ErrFailedToGetAddresses,
		},
		{
			description:    "empty address list",
			expected:       nil,
			interfaceAddrs: mock.NewNilInterfaceAddrs(),
			error:          ErrNoPrivateAddress,
		},
		{
			description:    "success",
			expected:       net.IP{192, 168, 0, 1},
			interfaceAddrs: mock.NewSuccessfulInterfaceAddrs(),
			error:          nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := privateIPv4(tc.interfaceAddrs)

			if !errors.Is(err, tc.error) {
				t.Fatalf("unexpected error: %v", err)
			}

			if !net.IP.Equal(actual, tc.expected) {
				t.Errorf("unexpected ip: %s", actual)
			}
		})
	}
}

func TestLower16BitPrivateIP(t *testing.T) {
	testCases := []struct {
		description    string
		expected       int
		interfaceAddrs types.InterfaceAddrs
		error          error
	}{
		{
			description:    "returns an error",
			expected:       0,
			interfaceAddrs: mock.NewFailingInterfaceAddrs(),
			error:          mock.ErrFailedToGetAddresses,
		},
		{
			description:    "empty address list",
			expected:       0,
			interfaceAddrs: mock.NewNilInterfaceAddrs(),
			error:          ErrNoPrivateAddress,
		},
		{
			description:    "success",
			expected:       1,
			interfaceAddrs: mock.NewSuccessfulInterfaceAddrs(),
			error:          nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := lower16BitPrivateIP(tc.interfaceAddrs)

			if !errors.Is(err, tc.error) {
				t.Fatalf("unexpected error: %v", err)
			}

			if actual != tc.expected {
				t.Errorf("unexpected ip: %d", actual)
			}
		})
	}
}

func TestToTime(t *testing.T) {
	start := time.Now()
	sf := newSonyflake(t, Settings{
		TimeUnit:  time.Millisecond,
		StartTime: start,
	})

	sf.now = func() time.Time { return start }
	id := nextID(t, sf)

	tm := sf.ToTime(id)
	diff := tm.Sub(start)
	if diff < 0 || diff >= time.Duration(sf.timeUnit) {
		t.Errorf("unexpected time: %v", diff)
	}
}

func TestComposeAndDecompose(t *testing.T) {
	now := time.Now()
	sf := newSonyflake(t, Settings{
		TimeUnit:  time.Millisecond,
		StartTime: now,
	})

	testCases := []struct {
		name      string
		time      time.Time
		sequence  int
		machineID int
	}{
		{
			name:      "zero values",
			time:      now,
			sequence:  0,
			machineID: 0,
		},
		{
			name:      "max sequence",
			time:      now,
			sequence:  1<<sf.bitsSequence - 1,
			machineID: 0,
		},
		{
			name:      "max machine id",
			time:      now,
			sequence:  0,
			machineID: 1<<sf.bitsMachine - 1,
		},
		{
			name:      "future time",
			time:      now.Add(time.Hour),
			sequence:  0,
			machineID: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := sf.Compose(tc.time, tc.sequence, tc.machineID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			parts := sf.Decompose(id)

			// Verify time part
			expectedTime := sf.toInternalTime(tc.time.UTC()) - sf.startTime
			if parts["time"] != expectedTime {
				t.Errorf("time mismatch: got %d, want %d", parts["time"], expectedTime)
			}

			// Verify sequence part
			if parts["sequence"] != int64(tc.sequence) {
				t.Errorf("sequence mismatch: got %d, want %d", parts["sequence"], tc.sequence)
			}

			// Verify machine id part
			if parts["machine"] != int64(tc.machineID) {
				t.Errorf("machine id mismatch: got %d, want %d", parts["machine"], tc.machineID)
			}

			// Verify id part
			if parts["id"] != id {
				t.Errorf("id mismatch: got %d, want %d", parts["id"], id)
			}
		})
	}
}

func TestCompose_ReturnsError(t *testing.T) {
	start := time.Now()
	sf := newSonyflake(t, Settings{StartTime: start})

	testCases := []struct {
		name      string
		time      time.Time
		sequence  int
		machineID int
		err       error
	}{
		{
			name:      "start time ahead",
			time:      start.Add(-time.Second),
			sequence:  0,
			machineID: 0,
			err:       ErrStartTimeAhead,
		},
		{
			name:      "over time limit",
			time:      start.Add(time.Duration(175) * year),
			sequence:  0,
			machineID: 0,
			err:       ErrOverTimeLimit,
		},
		{
			name:      "invalid sequence",
			time:      start,
			sequence:  1 << sf.bitsSequence,
			machineID: 0,
			err:       ErrInvalidSequence,
		},
		{
			name:      "invalid machine id",
			time:      start,
			sequence:  0,
			machineID: 1 << sf.bitsMachine,
			err:       ErrInvalidMachineID,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sf.Compose(tc.time, tc.sequence, tc.machineID)
			if !errors.Is(err, tc.err) {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
