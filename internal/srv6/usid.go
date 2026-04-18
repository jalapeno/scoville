package srv6

import (
	"fmt"
	"net/netip"
)

// SIDItem pairs a SID value with its behavior type and structural metadata.
// This is the unit the segment list builder passes to the packing layer.
type SIDItem struct {
	Value     string
	Behavior  BehaviorType
	Structure *SIDStructure // nil for SIDs without structure info
}

// TryPackUSID attempts to compress a slice of SIDItems into one or more uSID
// container addresses. If compression is possible the shorter container list
// is returned; otherwise the original values are returned unchanged.
//
// # Slot extraction — behavior-aware
//
// With F3216 (LocatorBlockLen=32, LocatorNodeLen=16, FunctionLen=16) the SID
// layout is:
//
//	[ block 32b ][ node 16b ][ function 16b ][ zeros ]
//	  fc00:0000   0003        e001
//
// The value packed into each 16-bit slot depends on the SID's behavior:
//
//   - uA (End.X family): pack the FUNCTION bits (offset blockLen+nodeLen,
//     length funcLen).  e.g. fc00:0:3:e001:: → slot value e001
//
//   - uN (End family):   pack the NODE LOCATOR bits (offset blockLen,
//     length nodeLen).  e.g. fc00:0:4:: → slot value 0004
//
// A three-hop path leaf-1→spine-1→leaf-2 therefore produces:
//
//	[ leaf-1 uA e001 ][ spine-1 uA e002 ][ leaf-2 uN 0004 ]
//	→  fc00:0:e001:e002:0004::
//
// # Compression conditions
//
//   - Every SIDItem must have a non-nil SIDStructure.
//   - All items must share the same LocatorBlockLen.
//   - nodeLen must equal funcLen (both define the slot width in the container).
//   - blockLen and slotLen must be byte-aligned.
//   - At least two SIDs must fit in a single container (otherwise no benefit).
func TryPackUSID(items []SIDItem) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	// Validate structure presence and consistency.
	var blockLen, slotLen uint8
	for i, item := range items {
		if item.Structure == nil {
			return FallbackValues(items), nil
		}
		s := item.Structure
		if i == 0 {
			blockLen = s.LocatorBlockLen
			slotLen = s.FunctionLen // slot width = function field width
		} else {
			if s.LocatorBlockLen != blockLen {
				return FallbackValues(items), nil
			}
			if s.FunctionLen != slotLen {
				return FallbackValues(items), nil
			}
		}
		// nodeLen must equal funcLen so that uN and uA slots are the same width.
		if s.LocatorNodeLen != s.FunctionLen {
			return FallbackValues(items), nil
		}
	}

	if slotLen == 0 {
		return FallbackValues(items), nil
	}

	// Require byte-alignment.
	if blockLen%8 != 0 || slotLen%8 != 0 {
		return FallbackValues(items), nil
	}

	blockBytes := int(blockLen / 8)
	slotBytes := int(slotLen / 8)
	totalBytes := 16 // 128-bit IPv6

	if blockBytes+slotBytes > totalBytes {
		return FallbackValues(items), nil
	}

	capacity := (totalBytes - blockBytes) / slotBytes
	if capacity < 2 {
		return FallbackValues(items), nil
	}

	// Extract the common block from the first SID.
	block, err := blockBytesFrom(items[0].Value, blockBytes)
	if err != nil {
		return FallbackValues(items), nil
	}

	var containers []string
	for i := 0; i < len(items); i += capacity {
		end := i + capacity
		if end > len(items) {
			end = len(items)
		}
		chunk := items[i:end]

		var c [16]byte
		copy(c[:blockBytes], block)

		for j, item := range chunk {
			slotValue, err := extractSlot(item, blockBytes, slotBytes)
			if err != nil {
				return FallbackValues(items), nil
			}
			offset := blockBytes + j*slotBytes
			copy(c[offset:offset+slotBytes], slotValue)
		}

		addr := netip.AddrFrom16(c)
		containers = append(containers, addr.String())
	}

	return containers, nil
}

// extractSlot returns the slotBytes bytes to pack for a single SIDItem.
//
//   - uA behaviors (End.X family) and uDT/uDX behaviors (End.DT*/End.DX*):
//     extract the function field at byteOffset = blockBytes + nodeBytes.
//     For uA this is the adjacency function (e.g. 0xe001); for uDT/uDX this
//     is the tenant VRF identifier (e.g. 0xd001).
//   - uN behaviors (End family) and everything else: extract the node locator
//     field at byteOffset = blockBytes.
func extractSlot(item SIDItem, blockBytes, slotBytes int) ([]byte, error) {
	nodeBytes := slotBytes // nodeLen == funcLen == slotLen (validated above)

	var byteOffset int
	switch item.Behavior {
	case BehaviorEndX, BehaviorEndXPSP, BehaviorEndXUSP,
		BehaviorEndDT4, BehaviorEndDT6, BehaviorEndDT46,
		BehaviorEndDX4, BehaviorEndDX6:
		// uA and uDT/uDX: the interesting bits are in the function field,
		// which follows the node locator. For uA this is the adjacency function
		// (e.g. e001); for uDT/uDX this is the tenant VRF identifier (e.g. d001).
		byteOffset = blockBytes + nodeBytes
	default:
		// uN (End) and unknown behaviors: extract the node locator field.
		byteOffset = blockBytes
	}

	return sidBytesAt(item.Value, byteOffset, slotBytes)
}

// --- helpers -------------------------------------------------------------

// FallbackValues returns the raw SID value strings from a SIDItem slice,
// used when compression is not applicable.
func FallbackValues(items []SIDItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Value
	}
	return out
}

// blockBytesFrom parses an IPv6 address string and returns the first
// blockBytes bytes as a slice.
func blockBytesFrom(addr string, blockBytes int) ([]byte, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", addr, err)
	}
	raw := a.As16()
	b := make([]byte, blockBytes)
	copy(b, raw[:blockBytes])
	return b, nil
}

// sidBytesAt parses an IPv6 address and extracts length bytes starting at
// byteOffset.
func sidBytesAt(addr string, byteOffset, length int) ([]byte, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", addr, err)
	}
	raw := a.As16()
	if byteOffset+length > 16 {
		return nil, fmt.Errorf("SID %q: offset %d + len %d exceeds 16 bytes", addr, byteOffset, length)
	}
	b := make([]byte, length)
	copy(b, raw[byteOffset:byteOffset+length])
	return b, nil
}
