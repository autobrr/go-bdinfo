package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/autobrr/go-bdinfo/internal/fs/udf"
)

func main() {
	iso := flag.String("iso", "", "path to UDF ISO")
	flag.Parse()
	if *iso == "" {
		log.Fatal("-iso required")
	}

	r, err := udf.NewReader(*iso)
	if err != nil {
		log.Fatalf("NewReader: %v", err)
	}
	defer r.Close()

	fmt.Printf("label=%q blockSize=%d partitionStart=%d fileSetLocation=%d\n", r.GetVolumeLabel(), r.BlockSize(), r.PartitionStart(), r.FileSetLocation())
	fmt.Printf("partitionMaps=%v\n", r.DebugPartitionMaps())
	fmt.Printf("rootICB: extentLen=%d lbn=%d pref=%d\n", r.RootICB().ExtentLength, r.RootICB().ExtentLocation.LogicalBlockNumber, r.RootICB().ExtentLocation.PartitionReferenceNumber)

	dir, err := r.ReadDirectory("/")
	if err != nil {
		fmt.Printf("ReadDirectory(/) err: %v\n", err)
		return
	}

	dirs, err := dir.GetDirectories()
	if err != nil {
		fmt.Printf("GetDirectories err: %v\n", err)
		return
	}
	fmt.Printf("root dirs (%d):\n", len(dirs))
	for _, d := range dirs {
		fmt.Printf("- %q\n", d.Name)
	}

	files, err := dir.GetFiles()
	if err != nil {
		fmt.Printf("GetFiles err: %v\n", err)
		return
	}
	fmt.Printf("root files (%d):\n", len(files))
	for _, f := range files {
		fmt.Printf("- %q size=%d\n", f.Name, f.Size())
	}

	pl, err := r.ReadDirectory("/BDMV/PLAYLIST")
	if err != nil {
		fmt.Printf("ReadDirectory(/BDMV/PLAYLIST) err: %v\n", err)
		return
	}
	plFiles, err := pl.GetFiles()
	if err != nil {
		fmt.Printf("GetFiles(/BDMV/PLAYLIST) err: %v\n", err)
		return
	}
	fmt.Printf("PLAYLIST files (%d):\n", len(plFiles))
	for _, f := range plFiles {
		rc, err := f.Open()
		if err != nil {
			fmt.Printf("- %q open err: %v\n", f.Name, err)
			continue
		}
		buf := make([]byte, 8)
		n, rerr := rc.Read(buf)
		_ = rc.Close()
		if rerr != nil && n == 0 {
			fmt.Printf("- %q read err: %v\n", f.Name, rerr)
			continue
		}
		fmt.Printf("- %q head=%q\n", f.Name, buf[:n])
	}

	ci, err := r.ReadDirectory("/BDMV/CLIPINF")
	if err != nil {
		fmt.Printf("ReadDirectory(/BDMV/CLIPINF) err: %v\n", err)
		return
	}
	ciFiles, err := ci.GetFiles()
	if err != nil {
		fmt.Printf("GetFiles(/BDMV/CLIPINF) err: %v\n", err)
		return
	}
	fmt.Printf("CLIPINF files (%d):\n", len(ciFiles))
	for i, f := range ciFiles {
		if i >= 10 {
			break
		}
		rc, err := f.Open()
		if err != nil {
			fmt.Printf("- %q open err: %v\n", f.Name, err)
			continue
		}
		buf := make([]byte, 8)
		n, rerr := rc.Read(buf)
		_ = rc.Close()
		if rerr != nil && n == 0 {
			fmt.Printf("- %q read err: %v\n", f.Name, rerr)
			continue
		}
		fmt.Printf("- %q head=%q size=%d\n", f.Name, buf[:n], f.Size())
	}

	st, err := r.ReadDirectory("/BDMV/STREAM")
	if err != nil {
		fmt.Printf("ReadDirectory(/BDMV/STREAM) err: %v\n", err)
		return
	}
	stFiles, err := st.GetFiles()
	if err != nil {
		fmt.Printf("GetFiles(/BDMV/STREAM) err: %v\n", err)
		return
	}
	for _, f := range stFiles {
		if strings.EqualFold(f.Name, "00002.m2ts") {
			fmt.Printf("STREAM %q size=%d\n", f.Name, f.Size())
			break
		}
	}
}
