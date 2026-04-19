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
// # Slot layout — F3216 example (blockLen=32, nodeLen=16)
//
// Each SID contributes one slot of width (nodeLen + maxFuncLen) bits, where
// maxFuncLen is the largest FunctionLen seen across all items. Using the maximum
// keeps the slot width consistent so uN and uA SIDs can coexist in one container:
//
//   - uA (funcLen=16): slot = bytes [blockBytes : blockBytes+slotBytes]
//     = node(16) + function(16), e.g. fc00:0:1:e002:: → 0x0001e002
//   - uN (funcLen=0):  slot = bytes [blockBytes : blockBytes+slotBytes]
//     = node(16) + 0x0000,      e.g. fc00:0:28::     → 0x00280000
//
// For a mixed four-hop path (uA, uA, uN, uN) on F3216 the slot width is
// 32 bits and capacity is (128-32)/32 = 3 SIDs per container, so two
// containers are produced:
//
//	Container 1: fc00:0:1:e002:4:e004:16:0   (xrd01 uA, xrd04 uA, xrd16 uN)
//	Container 2: fc00:0:28::                  (xrd28 uN)
//
// For an all-uN path (maxFuncLen=0) the slot width is 16 bits and capacity
// is 6 SIDs per container, so a four-hop path fits in a single container:
//
//	Container:   fc00:0:28:16:4:1::
//
// The outer IPv6 DA is always Container 0. When there are multiple containers
// the caller must build an SRH carrying containers 1..N-1.
//
// # Compression conditions
//
//   - Every SIDItem must have a non-nil SIDStructure.
//   - All items must share the same LocatorBlockLen and LocatorNodeLen.
//   - blockLen and (nodeLen+maxFuncLen) must be byte-aligned.
//   - At least one item must fit alongside the block in 128 bits.
func TryPackUSID(items []SIDItem) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	// --- Pass 1: validate structure consistency and compute slot width -------
	var blockLen, nodeLen, maxFuncLen uint8
	for i, item := range items {
		if item.Structure == nil {
			return FallbackValues(items), nil
		}
		s := item.Structure
		if i == 0 {
			blockLen = s.LocatorBlockLen
			nodeLen = s.LocatorNodeLen
		} else if s.LocatorBlockLen != blockLen || s.LocatorNodeLen != nodeLen {
			return FallbackValues(items), nil
		}
		if s.FunctionLen > maxFuncLen {
			maxFuncLen = s.FunctionLen
		}
	}

	// Slot width = nodeLen + maxFuncLen. For all-uN paths maxFuncLen=0 so
	// slots are nodeLen wide; for mixed uA+uN paths slots are wider.
	slotLen := nodeLen + maxFuncLen
	if slotLen == 0 || blockLen%8 != 0 || slotLen%8 != 0 {
		return FallbackValues(items), nil
	}

	blockBytes := int(blockLen / 8)
	slotBytes := int(slotLen / 8)

	if blockBytes+slotBytes > 16 {
		return FallbackValues(items), nil
	}
	capacity := (16 - blockBytes) / slotBytes
	if capacity < 1 {
		return FallbackValues(items), nil
	}

	// --- Pass 2: extract common block prefix --------------------------------
	block, err := blockBytesFrom(items[0].Value, blockBytes)
	if err != nil {
		return FallbackValues(items), nil
	}

	// --- Pass 3: pack into containers ---------------------------------------
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
			// The per-hop slot is bytes [blockBytes : blockBytes+slotBytes]:
			//   uA → node(nodeLen) + function(funcLen)   e.g. 0001:e002
			//   uN → node(nodeLen) + zeros(maxFuncLen-0) e.g. 0028:0000
			// Since function bytes are already zero in a uN address this
			// extraction works uniformly for both behaviors.
			slot, err := sidBytesAt(item.Value, blockBytes, slotBytes)
			if err != nil {
				return FallbackValues(items), nil
			}
			offset := blockBytes + j*slotBytes
			copy(c[offset:offset+slotBytes], slot)
		}

		containers = append(containers, netip.AddrFrom16(c).String())
	}
	return containers, nil
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
