package sonyflake

import (
	"net"
	"testing"
	"time"

	"github.com/sony/sonyflake/mock"
)

var (
	privateIP1 = net.IPv4(192, 168, 10, 1).To4()
	privateIP2 = net.IPv4(10, 0, 0, 5).To4()
	privateIP3 = net.IPv4(172, 16, 4, 9).To4()
)

// TestDefaultMachineID_StableAcrossInterfaceReorder verifies that reordering
// the network interfaces (the order in which the OS enumerates them) does not
// change the machine id.
func TestDefaultMachineID_StableAcrossInterfaceReorder(t *testing.T) {
	first := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2, privateIP3)
	reordered := mock.NewMultipleInterfaceAddrs(privateIP3, privateIP1, privateIP2)

	idBefore, err := defaultMachineID(first)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	idAfter, err := defaultMachineID(reordered)
	if err != nil {
		t.Fatalf("failed to get machine id after reorder: %v", err)
	}

	if idBefore != idAfter {
		t.Errorf("machine id changed after interface reorder: before=%d after=%d", idBefore, idAfter)
	}
}

// TestDefaultMachineID_StableWhenInterfaceAdded verifies that adding a new
// network interface does not change the machine id derived from the
// previously existing interfaces.
func TestDefaultMachineID_StableWhenInterfaceAdded(t *testing.T) {
	original := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	withNewNIC := mock.NewMultipleInterfaceAddrs(privateIP3, privateIP1, privateIP2)

	idBefore, err := defaultMachineID(original)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	idAfter, err := defaultMachineID(withNewNIC)
	if err != nil {
		t.Fatalf("failed to get machine id after adding an interface: %v", err)
	}

	if idBefore != idAfter {
		t.Errorf("machine id changed after adding an interface: before=%d after=%d", idBefore, idAfter)
	}
}

// TestDefaultMachineID_DeterministicAndInRange verifies that the derivation
// is reproducible and always produces an id that fits the machine id bits.
func TestDefaultMachineID_DeterministicAndInRange(t *testing.T) {
	interfaceAddrs := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)

	first, err := defaultMachineID(interfaceAddrs)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	second, err := defaultMachineID(interfaceAddrs)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	if first != second {
		t.Errorf("machine id is not deterministic: %d vs %d", first, second)
	}

	if uint64(first) >= 1<<BitLenMachineID {
		t.Errorf("machine id %d exceeds %d bits", first, BitLenMachineID)
	}
}

// TestDefaultMachineID_DeduplicatesAddresses verifies that duplicated
// addresses do not change the derived machine id.
func TestDefaultMachineID_DeduplicatesAddresses(t *testing.T) {
	unique := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	duplicated := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2, privateIP1)

	idUnique, err := defaultMachineID(unique)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	idDuplicated, err := defaultMachineID(duplicated)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	if idUnique != idDuplicated {
		t.Errorf("machine id changed because of duplicated addresses: %d vs %d", idUnique, idDuplicated)
	}
}

// TestDefaultMachineID_NoPrivateAddress verifies the error returned when the
// host has no usable private address.
func TestDefaultMachineID_NoPrivateAddress(t *testing.T) {
	if _, err := defaultMachineID(mock.NewNilInterfaceAddrs()); err != ErrNoPrivateAddress {
		t.Fatalf("expected ErrNoPrivateAddress, got: %v", err)
	}
}

// TestSonyflake_StableMachineIDAcrossRestart reproduces the reported
// scenario: a Sonyflake instance is created twice (simulating a restart) with
// the network interfaces changed in between. The default machine id must stay
// the same, otherwise the restarted instance could collide with another node
// whose machine id it now takes over.
func TestSonyflake_StableMachineIDAcrossRestart(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	originalAddrs := defaultInterfaceAddrs
	defer func() { defaultInterfaceAddrs = originalAddrs }()

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	first, err := New(Settings{StartTime: start})
	if err != nil {
		t.Fatalf("failed to create first instance: %v", err)
	}

	// Restart with the network interfaces reordered and a new one added.
	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP3, privateIP2, privateIP1)
	second, err := New(Settings{StartTime: start})
	if err != nil {
		t.Fatalf("failed to create second instance: %v", err)
	}

	if first.machineID != second.machineID {
		t.Fatalf("machine id changed across restart: before=%d after=%d", first.machineID, second.machineID)
	}
}

// TestSonyflake_DistinctNodesProduceDistinctIDs verifies that two different
// hosts (different private address sets) derive different machine ids, so ids
// generated in the same time slot across nodes never collide.
func TestSonyflake_DistinctNodesProduceDistinctIDs(t *testing.T) {
	start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	originalAddrs := defaultInterfaceAddrs
	defer func() { defaultInterfaceAddrs = originalAddrs }()

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	nodeA, err := New(Settings{StartTime: start})
	if err != nil {
		t.Fatalf("failed to create node A: %v", err)
	}

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(net.IPv4(192, 168, 20, 2).To4())
	nodeB, err := New(Settings{StartTime: start})
	if err != nil {
		t.Fatalf("failed to create node B: %v", err)
	}

	if nodeA.machineID == nodeB.machineID {
		t.Fatalf("distinct nodes derived the same machine id: %d", nodeA.machineID)
	}

	idA, err := nodeA.NextID()
	if err != nil {
		t.Fatalf("failed to generate id on node A: %v", err)
	}

	idB, err := nodeB.NextID()
	if err != nil {
		t.Fatalf("failed to generate id on node B: %v", err)
	}

	if idA == idB {
		t.Fatalf("distinct nodes generated the same id: %d", idA)
	}
}

// TestSonyflake_CheckMachineID verifies that the derived machine id can be
// validated with CheckMachineID without any manual configuration.
func TestSonyflake_CheckMachineID(t *testing.T) {
	originalAddrs := defaultInterfaceAddrs
	defer func() { defaultInterfaceAddrs = originalAddrs }()

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)

	expected, err := defaultMachineID(defaultInterfaceAddrs)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	sf, err := New(Settings{
		CheckMachineID: func(id uint16) bool {
			return id == expected
		},
	})
	if err != nil {
		t.Fatalf("failed to create sonyflake: %v", err)
	}
	if sf.machineID != expected {
		t.Errorf("unexpected machine id: got %d want %d", sf.machineID, expected)
	}
}
