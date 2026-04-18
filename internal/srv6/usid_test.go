package srv6

import (
	"testing"
)

// --- helpers ----------------------------------------------------------------

func f3216() *SIDStructure {
	return &SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 16}
}

func ua(v string) SIDItem { return SIDItem{Value: v, Behavior: BehaviorEndX, Structure: f3216()} }
func un(v string) SIDItem { return SIDItem{Value: v, Behavior: BehaviorEnd, Structure: f3216()} }
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
	// F3216 extraction:
	//   ua("fc00:0:3:e001::") → function field bytes 6-7 = 0xe001
	//   ua("fc00:0:2:e002::") → function field bytes 6-7 = 0xe002
	//   un("fc00:0:4::")      → node locator bytes 4-5   = 0x0004
	// Container: fc00:0000:e001:e002:0004:0000:0000:0000 = fc00:0:e001:e002:4::
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
	const want = "fc00:0:e001:e002:4::"
	if got[0] != want {
		t.Errorf("want %s, got %s", want, got[0])
	}
}

func TestTryPackUSID_WithUDT(t *testing.T) {
	// Multi-tenant carrier: uA + uA + uN + uDT.
	// udt("fc00:0:4:d001::") → function field bytes 6-7 = 0xd001
	// Container: fc00:0:e001:e002:4:d001::
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
	if len(got) != 1 {
		t.Fatalf("want 1 container, got %d: %v", len(got), got)
	}
	const want = "fc00:0:e001:e002:4:d001::"
	if got[0] != want {
		t.Errorf("want %s, got %s", want, got[0])
	}
}

func TestTryPackUSID_Overflow(t *testing.T) {
	// F3216: capacity = (16-4)/2 = 6 slots per container.
	// 7 items → container 1 holds items 0-5, container 2 holds item 6.
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
	if len(got) != 2 {
		t.Fatalf("want 2 containers for 7 items, got %d: %v", len(got), got)
	}
	const want0 = "fc00:0:e001:e002:e003:e004:e005:e006"
	const want1 = "fc00:0:e007::"
	if got[0] != want0 {
		t.Errorf("container 0: want %s, got %s", want0, got[0])
	}
	if got[1] != want1 {
		t.Errorf("container 1: want %s, got %s", want1, got[1])
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

func TestTryPackUSID_FallbackUnequalNodeFunc(t *testing.T) {
	// nodeLen != funcLen → uN and uA slots would be different widths; fall back.
	bad := &SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 32}
	items := []SIDItem{
		{Value: "fc00:0:3:e001::", Behavior: BehaviorEndX, Structure: bad},
	}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "fc00:0:3:e001::" {
		t.Errorf("unexpected result: %v", got)
	}
}

func TestTryPackUSID_SingleItemPacked(t *testing.T) {
	// A single uN SID still gets packed (capacity=6 >= 2).
	// The result is structurally identical to the input since the slot value
	// (node locator 0x0004) is already at the same position in the container.
	items := []SIDItem{un("fc00:0:4::")}
	got, err := TryPackUSID(items)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 container, got %d", len(got))
	}
	// Block fc00:0000 + node locator 0004 + zeros = fc00:0:4::
	if got[0] != "fc00:0:4::" {
		t.Errorf("want fc00:0:4::, got %s", got[0])
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
