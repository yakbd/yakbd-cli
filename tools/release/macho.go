package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
)

// fatAlign is the alignment of every slice in a universal binary, as a power of two: 2^14 = 16 KiB, a multiple of the
// page size of both architectures.
const fatAlign = 14

// fatMachO joins thin Mach-O programs into one universal ("fat") Mach-O file, in the given order. The layout is the
// one Go's debug/macho reads (fat.go: NewFatFile): a big-endian header, magic 0xcafebabe (macho.MagicFat) and the
// number of slices, then one record per slice (cpu type, cpu subtype, offset, size, alignment as a power of two,
// each a big-endian uint32), then each program unchanged at its offset, aligned to 2^fatAlign. The cpu type and
// subtype are each program's own header fields. Each slice keeps its own code signature, which covers only the slice.
func fatMachO(thin ...[]byte) ([]byte, error) {
	const headerSize, archSize = 8, 20
	type slice struct {
		cpu    macho.Cpu
		subCpu uint32
		data   []byte
	}
	var slices []slice
	for i, b := range thin {
		f, err := macho.NewFile(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("universal binary: program %d: %v", i, err)
		}
		for _, s := range slices {
			if s.cpu == f.Cpu {
				return nil, fmt.Errorf("universal binary: two programs for %v", f.Cpu)
			}
		}
		slices = append(slices, slice{f.Cpu, f.SubCpu, b})
	}
	if len(slices) == 0 {
		return nil, fmt.Errorf("universal binary: no program")
	}
	align := uint64(1) << fatAlign
	up := func(n uint64) uint64 { return (n + align - 1) &^ (align - 1) }
	var out bytes.Buffer
	be := binary.BigEndian
	out.Write(be.AppendUint32(nil, macho.MagicFat))
	out.Write(be.AppendUint32(nil, uint32(len(slices))))
	offset := up(headerSize + archSize*uint64(len(slices)))
	offsets := make([]uint64, len(slices))
	for i, s := range slices {
		if offset+uint64(len(s.data)) > 1<<32-1 {
			return nil, fmt.Errorf("universal binary: larger than 4 GiB")
		}
		offsets[i] = offset
		for _, v := range []uint32{uint32(s.cpu), s.subCpu, uint32(offset), uint32(len(s.data)), fatAlign} {
			out.Write(be.AppendUint32(nil, v))
		}
		offset = up(offset + uint64(len(s.data)))
	}
	for i, s := range slices {
		out.Write(make([]byte, int(offsets[i])-out.Len()))
		out.Write(s.data)
	}
	return out.Bytes(), nil
}
