package srv6

import (
	"testing"
)

// --- helpers ----------------------------------------------------------------

// f3216 returns the SIDStructure for an F3216 uA SID (block=32, node=16, func=16).
func f3216() *SIDStructure {
	return &SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 16}
}

// f3216uN returns the SIDStructure for an F3216 uN SID (block=32, node=16, func=0).
// Real IOS-XR uN SIDs advertise FunctionLen=0 because micro-node SIDs have no
// function bits — the node locator IS the SID.
func f3216uN() *SIDStructure {
	return &SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 0}
}

func ua(v string) SIDItem  { return SIDItem{Value: v, Behavior: BehaviorEndX, Structure: f3216()} }
func un(v string) SIDItem  { return SIDItem{Value: v, Behavior: BehaviorEnd, Structure: f3216uN()} }
func udt(v string) SIDItem { return SIDItem{Value: v, Behavior: BehaviorEndDT6, Structure: f3216()} }
func noStruct(v string) SIDItem { return SIDItem{Value: v, Behavior: BehaviorEnd} }

// --- TryPackUSID tests -------------------------------------------------------

func TestTryPackUSID_Empty(t *testing.T) {
	got, err := TryPackUSID(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestTryPackUSID_TwoUAOneUN(t *testing.T) {
	// Core two-tier fabric case: leaf→spine uA, spine→leaf uA, dest uN.
	// uA funcLen=16, uN funcLen=0 → maxFuncLen=16; slot=32bits=4bytes, capacity=3.
	// F3216 slot extraction (bytes [4:8] from each SID):
	//   ua("fc00:0:3:e001::") → 0x00, 0x03, 0xe0, 0x01
	//   ua("fc00:0:2:e002::") → 0x00, 0x02, 0xe0, 0x02
	//   un("fc00:0:4::")      → 0x00, 0x04, 0x00, 0x00
	// Container: fc00:0000:0003:e001:0002:e002:0004:0000 = fc00:0:3:e001:2:e002:4:0
	items := []SIDItem{
		ua("fc00:0:3:e001::"),
		ua("fc00:0:2:e002::"),
		un("fc00:0:4::"),
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 container, got %d: %v", len(got), got)
	}
	const want = "fc00:0:3:e001:2:e002:4:0"
	if got[0] != want {
		t.Errorf("want %s, got %s", want, got[0])
	}
}

func TestTryPackUSID_WithUDT(t *testing.T) {
	// Multi-tenant carrier: uA + uA + uN + uDT6.
	// maxFuncLen=16 (uA and uDT both have funcLen=16); slot=32bits=4bytes, capacity=3.
	// 4 items → 2 containers: [uA, uA, uN] then [uDT].
	// Container 1 slots (bytes [4:8]):
	//   ua("fc00:0:3:e001::") → 0003:e001
	//   ua("fc00:0:2:e002::") → 0002:e002
	//   un("fc00:0:4::")      → 0004:0000
	// Container 2 slot:
	//   udt("fc00:0:4:d001::") → 0004:d001
	items := []SIDItem{
		ua("fc00:0:3:e001::"),
		ua("fc00:0:2:e002::"),
		un("fc00:0:4::"),
		udt("fc00:0:4:d001::"),
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 containers for 4 items (capacity=3), got %d: %v", len(got), got)
	}
	const want0 = "fc00:0:3:e001:2:e002:4:0"
	const want1 = "fc00:0:4:d001::"
	if got[0] != want0 {
		t.Errorf("container 0: want %s, got %s", want0, got[0])
	}
	if got[1] != want1 {
		t.Errorf("container 1: want %s, got %s", want1, got[1])
	}
}

func TestTryPackUSID_Overflow(t *testing.T) {
	// All-uA: funcLen=16, slot=32bits=4bytes, capacity=(16-4)/4=3.
	// 7 items → 3 containers: items [0:3], [3:6], [6:7].
	// Container slots are bytes [4:8] from each SID, e.g.:
	//   ua("fc00:0:3:e001::") → 0x00,0x03,0xe0,0x01
	items := []SIDItem{
		ua("fc00:0:3:e001::"),
		ua("fc00:0:3:e002::"),
		ua("fc00:0:3:e003::"),
		ua("fc00:0:3:e004::"),
		ua("fc00:0:3:e005::"),
		ua("fc00:0:3:e006::"),
		ua("fc00:0:3:e007::"),
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 containers for 7 items (capacity=3), got %d: %v", len(got), got)
	}
	const want0 = "fc00:0:3:e001:3:e002:3:e003"
	const want1 = "fc00:0:3:e004:3:e005:3:e006"
	const want2 = "fc00:0:3:e007::"
	if got[0] != want0 {
		t.Errorf("container 0: want %s, got %s", want0, got[0])
	}
	if got[1] != want1 {
		t.Errorf("container 1: want %s, got %s", want1, got[1])
	}
	if got[2] != want2 {
		t.Errorf("container 2: want %s, got %s", want2, got[2])
	}
}

func TestTryPackUSID_FallbackNoStructure(t *testing.T) {
	// Any item missing SIDStructure → raw SIDs returned unchanged.
	items := []SIDItem{
		ua("fc00:0:3:e001::"),
		noStruct("fc00:0:4::"),
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 raw SIDs, got %d", len(got))
	}
	if got[0] != "fc00:0:3:e001::" || got[1] != "fc00:0:4::" {
		t.Errorf("unexpected fallback values: %v", got)
	}
}

func TestTryPackUSID_FallbackMismatchedBlock(t *testing.T) {
	// Different LocatorBlockLen values → incompatible; fall back.
	s48 := &SIDStructure{LocatorBlockLen: 48, LocatorNodeLen: 16, FunctionLen: 16}
	items := []SIDItem{
		ua("fc00:0:3:e001::"),
		{Value: "2001:db8::1", Behavior: BehaviorEndX, Structure: s48},
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 raw SIDs on mismatch, got %d: %v", len(got), got)
	}
}

func TestTryPackUSID_LargeFuncLen(t *testing.T) {
	// F3216 with funcLen=32: slot=nodeLen+funcLen=48bits=6bytes, capacity=(16-4)/6=2.
	// Two SIDs pack into one container using 6-byte slots (bytes [4:10]).
	//   "fc00:0:3:e001:beef::" → slot bytes 4-9: 00,03,e0,01,be,ef
	//   "fc00:0:4:d001:cafe::" → slot bytes 4-9: 00,04,d0,01,ca,fe
	// Container: fc00:0000 | 0003e001beef | 0004d001cafe
	//          = fc00:0:3:e001:beef:4:d001:cafe
	wide := &SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 32}
	items := []SIDItem{
		{Value: "fc00:0:3:e001:beef::", Behavior: BehaviorEndX, Structure: wide},
		{Value: "fc00:0:4:d001:cafe::", Behavior: BehaviorEndX, Structure: wide},
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 container, got %d: %v", len(got), got)
	}
	const want = "fc00:0:3:e001:beef:4:d001:cafe"
	if got[0] != want {
		t.Errorf("want %s, got %s", want, got[0])
	}
}

func TestTryPackUSID_SingleItemPacked(t *testing.T) {
	// A single uN SID: funcLen=0 → slot=16bits=2bytes, capacity=(16-4)/2=6.
	// Block fc00:0000 + slot bytes [4:6] = 0x00,0x04 → fc00:0:4::
	items := []SIDItem{un("fc00:0:4::")}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 container, got %d", len(got))
	}
	if got[0] != "fc00:0:4::" {
		t.Errorf("want fc00:0:4::, got %s", got[0])
	}
}

func TestTryPackUSID_AllUN(t *testing.T) {
	// All-uN path: funcLen=0 → slot=16bits=2bytes, capacity=6.
	// Four uN SIDs fit in one container.
	// Slot bytes [4:6] for each (IPv6 groups are hex, so "16" = 0x16, "28" = 0x28):
	//   un("fc00:0:1::") → 0x00,0x01
	//   un("fc00:0:4::") → 0x00,0x04
	//   un("fc00:0:16::") → 0x00,0x16
	//   un("fc00:0:28::") → 0x00,0x28
	// Container: fc00:0000:0001:0004:0016:0028:0000:0000 = fc00:0:1:4:16:28::
	items := []SIDItem{
		un("fc00:0:1::"),
		un("fc00:0:4::"),
		un("fc00:0:16::"),
		un("fc00:0:28::"),
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 container for 4 uN items (capacity=6), got %d: %v", len(got), got)
	}
	const want = "fc00:0:1:4:16:28::"
	if got[0] != want {
		t.Errorf("want %s, got %s", want, got[0])
	}
}

// --- FallbackValues ----------------------------------------------------------

func TestFallbackValues(t *testing.T) {
	items := []SIDItem{
		{Value: "fc00:0:3:e001::"},
		{Value: "fc00:0:4::"},
	}
	got := FallbackValues(items)
	if len(got) != 2 || got[0] != "fc00:0:3:e001::" || got[1] != "fc00:0:4::" {
		t.Errorf("unexpected fallback: %v", got)
	}
}
