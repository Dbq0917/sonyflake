package sonyflake

import (
	"net"
	"testing"

	"github.com/sony/sonyflake/v2/mock"
)

var (
	privateIP1 = net.IPv4(192, 168, 10, 1).To4()
	privateIP2 = net.IPv4(10, 0, 0, 5).To4()
	privateIP3 = net.IPv4(172, 16, 4, 9).To4()
)

// TestDefaultMachineID_StableAcrossInterfaceReorder verifies that reordering
// the network interfaces does not change the machine id.
func TestDefaultMachineID_StableAcrossInterfaceReorder(t *testing.T) {
	first := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2, privateIP3)
	reordered := mock.NewMultipleInterfaceAddrs(privateIP3, privateIP1, privateIP2)

	idBefore, err := defaultMachineID(first, defaultBitsMachine)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	idAfter, err := defaultMachineID(reordered, defaultBitsMachine)
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

	idBefore, err := defaultMachineID(original, defaultBitsMachine)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	idAfter, err := defaultMachineID(withNewNIC, defaultBitsMachine)
	if err != nil {
		t.Fatalf("failed to get machine id after adding an interface: %v", err)
	}

	if idBefore != idAfter {
		t.Errorf("machine id changed after adding an interface: before=%d after=%d", idBefore, idAfter)
	}
}

// TestDefaultMachineID_DeterministicAndInRange verifies reproducibility and
// that the id always fits the configured bit width.
func TestDefaultMachineID_DeterministicAndInRange(t *testing.T) {
	interfaceAddrs := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)

	first, err := defaultMachineID(interfaceAddrs, defaultBitsMachine)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	second, err := defaultMachineID(interfaceAddrs, defaultBitsMachine)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	if first != second {
		t.Errorf("machine id is not deterministic: %d vs %d", first, second)
	}

	if first < 0 || first >= 1<<defaultBitsMachine {
		t.Errorf("machine id %d out of range for %d bits", first, defaultBitsMachine)
	}
}

// TestDefaultMachineID_RespectsCustomBits verifies that the derived machine id
// is masked to fit a custom BitsMachineID setting.
func TestDefaultMachineID_RespectsCustomBits(t *testing.T) {
	interfaceAddrs := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2, privateIP3)

	for _, bits := range []int{1, 8, 10, 16, 20} {
		id, err := defaultMachineID(interfaceAddrs, bits)
		if err != nil {
			t.Fatalf("bits=%d: failed to get machine id: %v", bits, err)
		}
		if id < 0 || id >= 1<<bits {
			t.Errorf("bits=%d: machine id %d out of range", bits, id)
		}
	}
}

// TestDefaultMachineID_DeduplicatesAddresses verifies that duplicated
// addresses do not change the derived machine id.
func TestDefaultMachineID_DeduplicatesAddresses(t *testing.T) {
	unique := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	duplicated := mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2, privateIP1)

	idUnique, err := defaultMachineID(unique, defaultBitsMachine)
	if err != nil {
		t.Fatalf("failed to get machine id: %v", err)
	}

	idDuplicated, err := defaultMachineID(duplicated, defaultBitsMachine)
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
	if _, err := defaultMachineID(mock.NewNilInterfaceAddrs(), defaultBitsMachine); err != ErrNoPrivateAddress {
		t.Fatalf("expected ErrNoPrivateAddress, got: %v", err)
	}
}

// TestSonyflake_StableMachineIDAcrossRestart reproduces the reported
// scenario: a Sonyflake instance is created twice (simulating a restart) with
// the network interfaces changed in between. The default machine id must stay
// the same.
func TestSonyflake_StableMachineIDAcrossRestart(t *testing.T) {
	originalAddrs := defaultInterfaceAddrs
	defer func() { defaultInterfaceAddrs = originalAddrs }()

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	first, err := New(Settings{})
	if err != nil {
		t.Fatalf("failed to create first instance: %v", err)
	}

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP3, privateIP2, privateIP1)
	second, err := New(Settings{})
	if err != nil {
		t.Fatalf("failed to create second instance: %v", err)
	}

	if first.machine != second.machine {
		t.Fatalf("machine id changed across restart: before=%d after=%d", first.machine, second.machine)
	}
}

// TestSonyflake_DistinctNodesProduceDistinctIDs verifies that different hosts
// derive different machine ids, so ids generated in the same time slot across
// nodes never collide.
func TestSonyflake_DistinctNodesProduceDistinctIDs(t *testing.T) {
	originalAddrs := defaultInterfaceAddrs
	defer func() { defaultInterfaceAddrs = originalAddrs }()

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2)
	nodeA, err := New(Settings{})
	if err != nil {
		t.Fatalf("failed to create node A: %v", err)
	}

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(net.IPv4(192, 168, 20, 2).To4())
	nodeB, err := New(Settings{})
	if err != nil {
		t.Fatalf("failed to create node B: %v", err)
	}

	if nodeA.machine == nodeB.machine {
		t.Fatalf("distinct nodes derived the same machine id: %d", nodeA.machine)
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

// TestSonyflake_DefaultMachineIDWithCustomBits verifies that an instance
// created with a custom BitsMachineID derives a default machine id that fits.
func TestSonyflake_DefaultMachineIDWithCustomBits(t *testing.T) {
	originalAddrs := defaultInterfaceAddrs
	defer func() { defaultInterfaceAddrs = originalAddrs }()

	defaultInterfaceAddrs = mock.NewMultipleInterfaceAddrs(privateIP1, privateIP2, privateIP3)

	sf, err := New(Settings{BitsMachineID: 10})
	if err != nil {
		t.Fatalf("failed to create instance: %v", err)
	}

	if sf.machine < 0 || sf.machine >= 1<<10 {
		t.Errorf("machine id %d does not fit in 10 bits", sf.machine)
	}
}
