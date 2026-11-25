// Command genrsrc packs app.ico and app.manifest into a Windows x64 COFF .syso
// resource object so Go embeds native icons, PerMonitorV2 DPI awareness, and
// ComCtl32 v6 visual styles without requiring external C/SDK toolchains.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
)

const (
	rtIcon      uint16 = 3
	rtGroupIcon uint16 = 14
	rtManifest  uint16 = 24
	langNeutral uint16 = 0x0409
)

type resourceItem struct {
	typeID uint16
	nameID uint16
	data   []byte
}

func main() {
	icoPath := flag.String("ico", "app.ico", "path to .ico file")
	manifestPath := flag.String("manifest", "app.manifest", "path to .manifest file")
	outPath := flag.String("o", "rsrc_windows_amd64.syso", "output .syso path")
	flag.Parse()

	icoBytes, err := os.ReadFile(*icoPath)
	if err != nil {
		fatalf("read icon: %v", err)
	}
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		fatalf("read manifest: %v", err)
	}

	items, err := buildIconResources(icoBytes)
	if err != nil {
		fatalf("parse icon: %v", err)
	}
	items = append(items, resourceItem{
		typeID: rtManifest,
		nameID: 1,
		data:   manifestBytes,
	})

	coff, err := buildCOFF(items)
	if err != nil {
		fatalf("build coff: %v", err)
	}
	if err := os.WriteFile(*outPath, coff, 0644); err != nil {
		fatalf("write output: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func buildIconResources(ico []byte) ([]resourceItem, error) {
	if len(ico) < 6 {
		return nil, fmt.Errorf("icon file too short (%d bytes)", len(ico))
	}
	reserved := binary.LittleEndian.Uint16(ico[0:2])
	iconType := binary.LittleEndian.Uint16(ico[2:4])
	count := binary.LittleEndian.Uint16(ico[4:6])
	if reserved != 0 || iconType != 1 || count == 0 {
		return nil, fmt.Errorf("invalid ICO header (reserved=%d type=%d count=%d)", reserved, iconType, count)
	}
	if len(ico) < 6+int(count)*16 {
		return nil, fmt.Errorf("truncated ICO directory")
	}

	var grp bytes.Buffer
	_ = binary.Write(&grp, binary.LittleEndian, uint16(0))
	_ = binary.Write(&grp, binary.LittleEndian, uint16(1))
	_ = binary.Write(&grp, binary.LittleEndian, count)

	items := make([]resourceItem, 0, int(count)+1)
	for i := uint16(0); i < count; i++ {
		entry := ico[6+int(i)*16 : 6+int(i+1)*16]
		bytesInRes := binary.LittleEndian.Uint32(entry[8:12])
		imageOffset := binary.LittleEndian.Uint32(entry[12:16])
		if uint64(imageOffset)+uint64(bytesInRes) > uint64(len(ico)) {
			return nil, fmt.Errorf("ICO entry %d exceeds file bounds", i)
		}
		id := i + 1
		// GRPICONDIRENTRY copies the first 12 bytes of ICONDIRENTRY followed by uint16 resource ID.
		grp.Write(entry[:12])
		_ = binary.Write(&grp, binary.LittleEndian, id)

		items = append(items, resourceItem{
			typeID: rtIcon,
			nameID: id,
			data:   ico[imageOffset : imageOffset+bytesInRes],
		})
	}
	items = append(items, resourceItem{
		typeID: rtGroupIcon,
		nameID: 1,
		data:   grp.Bytes(),
	})
	return items, nil
}

func buildCOFF(items []resourceItem) ([]byte, error) {
	// Group items by sorted typeID: rtIcon (3), rtGroupIcon (14), rtManifest (24).
	typeOrder := []uint16{rtIcon, rtGroupIcon, rtManifest}
	byType := make(map[uint16][]resourceItem)
	for _, it := range items {
		byType[it.typeID] = append(byType[it.typeID], it)
	}
	var activeTypes []uint16
	for _, t := range typeOrder {
		if len(byType[t]) > 0 {
			activeTypes = append(activeTypes, t)
		}
	}

	// Calculate directory tree layout inside .rsrc:
	// Level 1: 1 root directory (16B) + len(activeTypes)*8B entries
	// Level 2: len(activeTypes) directories, total items entries
	// Level 3: len(items) language directories, each with 1 entry
	// Data entries: len(items) * 16B IMAGE_RESOURCE_DATA_ENTRY
	l1Size := 16 + len(activeTypes)*8
	l2Offset := l1Size
	l2Size := len(activeTypes)*16 + len(items)*8
	l3Offset := l2Offset + l2Size
	l3Size := len(items) * (16 + 8)
	dataEntriesOffset := l3Offset + l3Size
	dataEntriesSize := len(items) * 16
	payloadStart := align8(dataEntriesOffset + dataEntriesSize)

	var rsrc bytes.Buffer
	writeDirHeader := func(idEntries uint16) {
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // Characteristics
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // TimeDateStamp
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(0)) // MajorVersion
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(0)) // MinorVersion
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(0)) // NumberOfNamedEntries
		_ = binary.Write(&rsrc, binary.LittleEndian, idEntries)
	}
	writeDirEntry := func(id uint16, offset uint32, isSubdir bool) {
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(id))
		if isSubdir {
			offset |= 0x80000000
		}
		_ = binary.Write(&rsrc, binary.LittleEndian, offset)
	}

	// Level 1 (Root)
	writeDirHeader(uint16(len(activeTypes)))
	curL2 := uint32(l2Offset)
	for _, t := range activeTypes {
		writeDirEntry(t, curL2, true)
		curL2 += uint32(16 + len(byType[t])*8)
	}

	// Level 2 (Names/IDs per Type)
	curL3 := uint32(l3Offset)
	var orderedItems []resourceItem
	for _, t := range activeTypes {
		list := byType[t]
		writeDirHeader(uint16(len(list)))
		for _, it := range list {
			writeDirEntry(it.nameID, curL3, true)
			curL3 += 24
			orderedItems = append(orderedItems, it)
		}
	}

	// Level 3 (Language per Item)
	curDataEntry := uint32(dataEntriesOffset)
	for range orderedItems {
		writeDirHeader(1)
		writeDirEntry(langNeutral, curDataEntry, false)
		curDataEntry += 16
	}

	// IMAGE_RESOURCE_DATA_ENTRY array + Relocations
	type coffReloc struct {
		VirtualAddress   uint32
		SymbolTableIndex uint32
		Type             uint16
	}
	relocs := make([]coffReloc, 0, len(orderedItems))
	payloadOffsets := make([]uint32, len(orderedItems))
	curPayload := uint32(payloadStart)
	for i, it := range orderedItems {
		payloadOffsets[i] = curPayload
		curPayload = uint32(align8(int(curPayload) + len(it.data)))
	}

	for i, it := range orderedItems {
		entryOffset := uint32(rsrc.Len())
		relocs = append(relocs, coffReloc{
			VirtualAddress:   entryOffset, // OffsetToData is at +0 in IMAGE_RESOURCE_DATA_ENTRY
			SymbolTableIndex: 0,           // .rsrc section symbol
			Type:             0x0003,      // IMAGE_REL_AMD64_ADDR32NB
		})
		_ = binary.Write(&rsrc, binary.LittleEndian, payloadOffsets[i]) // OffsetToData (addend)
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(len(it.data)))
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // CodePage
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // Reserved
	}

	for rsrc.Len() < payloadStart {
		rsrc.WriteByte(0)
	}
	for _, it := range orderedItems {
		rsrc.Write(it.data)
		padded := align8(rsrc.Len())
		for rsrc.Len() < padded {
			rsrc.WriteByte(0)
		}
	}

	rawSection := rsrc.Bytes()
	const fileHeaderSize = 20
	const sectionHeaderSize = 40
	rawOffset := uint32(fileHeaderSize + sectionHeaderSize)
	relocOffset := rawOffset + uint32(len(rawSection))
	symtabOffset := relocOffset + uint32(len(relocs)*10)

	var out bytes.Buffer
	// IMAGE_FILE_HEADER
	_ = binary.Write(&out, binary.LittleEndian, uint16(0x8664)) // IMAGE_FILE_MACHINE_AMD64
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))      // NumberOfSections
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))      // TimeDateStamp
	_ = binary.Write(&out, binary.LittleEndian, symtabOffset)   // PointerToSymbolTable
	_ = binary.Write(&out, binary.LittleEndian, uint32(1))      // NumberOfSymbols
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))      // SizeOfOptionalHeader
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))      // Characteristics

	// IMAGE_SECTION_HEADER (.rsrc)
	var secName [8]byte
	copy(secName[:], ".rsrc")
	out.Write(secName[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                 // VirtualSize
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                 // VirtualAddress
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(rawSection)))   // SizeOfRawData
	_ = binary.Write(&out, binary.LittleEndian, rawOffset)                 // PointerToRawData
	_ = binary.Write(&out, binary.LittleEndian, relocOffset)               // PointerToRelocations
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))                 // PointerToLineNumbers
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(relocs)))       // NumberOfRelocations
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))                 // NumberOfLineNumbers
	_ = binary.Write(&out, binary.LittleEndian, uint32(0x40000040))        // CNT_INITIALIZED_DATA | MEM_READ

	// Section Raw Data
	out.Write(rawSection)

	// COFF Relocations (10 bytes each, packed)
	for _, r := range relocs {
		_ = binary.Write(&out, binary.LittleEndian, r.VirtualAddress)
		_ = binary.Write(&out, binary.LittleEndian, r.SymbolTableIndex)
		_ = binary.Write(&out, binary.LittleEndian, r.Type)
	}

	// COFF Symbol Table: 1 entry (18 bytes) for ".rsrc"
	out.Write(secName[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // Value = 0 (start of section)
	_ = binary.Write(&out, binary.LittleEndian, int16(1))  // SectionNumber = 1
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // Type = 0
	out.WriteByte(3)                                       // StorageClass = IMAGE_SYM_CLASS_STATIC (3)
	out.WriteByte(0)                                       // NumberOfAuxSymbols = 0

	// COFF String Table (empty = uint32(4))
	_ = binary.Write(&out, binary.LittleEndian, uint32(4))

	return out.Bytes(), nil
}

func align8(n int) int {
	return (n + 7) &^ 7
}
