package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/partition/gpt"
)

func main() {
	espSz := int64(200 * 1024 * 1024) // 200MB

	sz := flag.Int64("size", int64(10*1024*1024*1024), "size of disk to create (size of efi partition will be added to this value)")
	rootfsContent := flag.String("rootfs", "", "path to content to rootfs")
	efiContent := flag.String("efi-content", "/boot/efi", "path to content to write to efi partition")

	flag.Parse()

	p := flag.Arg(0)
	if p == "" {
		panic("Requires 1 arg")
	}

	totalSz := espSz + *sz + 2048 + 1
	raw, err := diskfs.Create(p, totalSz, diskfs.Raw, diskfs.SectorSize512)
	if err != nil {
		panic(err)
	}

	espSectors := espSz / int64(diskfs.SectorSize512)
	espEnd := uint64(espSectors - 2048 + 1)

	totalSectors := totalSz / int64(diskfs.SectorSize512)

	tbl := gpt.Table{
		Partitions: []*gpt.Partition{
			{
				Start: 2048, End: espEnd, Type: gpt.EFISystemPartition, Name: "EFI System",
			},
			{
				Start: espEnd + 1, End: uint64(totalSectors), Type: gpt.LinuxFilesystem,
			},
		},
	}

	if err := raw.Partition(&tbl); err != nil {
		panic(err)
	}

	efiConfig := disk.FilesystemSpec{
		Partition: 1,
		FSType:    filesystem.TypeFat32,
	}

	rootfsConfig := disk.FilesystemSpec{
		Partition: 2,
		FSType:    filesystem.TypeExt4,
	}

	efiFS, err := raw.CreateFilesystem(efiConfig)
	if err != nil {
		panic(err)
	}

	rootfs, err := raw.CreateFilesystem(rootfsConfig)
	if err != nil {
		panic(err)
	}

	efiDir := os.DirFS(*efiContent)
	err = copyToFS(efiFS, efiDir)
	if err != nil {
		panic(err)
	}

	err = copyToFS(rootfs, os.DirFS(*rootfsContent))
	if err != nil {
		panic(err)
	}
}

func copyToFS(to filesystem.FileSystem, from fs.FS) error {
	return fs.WalkDir(from, ".", func(p string, dent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if dent.IsDir() {
			if err := to.Mkdir(filepath.Join("/", p)); err != nil {
				return fmt.Errorf("error creatign dir: %w", err)
			}
			return nil
		}

		r, err := from.Open(p)
		if err != nil {
			return fmt.Errorf("error opening file for read: %w", err)
		}
		defer r.Close()

		// diskfs lib requires O_RDWR since it seeems to incrrectly detect O_WRONLY as read-only
		w, err := to.OpenFile(filepath.Join("/", p), os.O_RDWR|os.O_CREATE)
		if err != nil {
			return fmt.Errorf("error creating file: %w", err)
		}
		defer w.Close()

		_, err = io.Copy(w, r)
		if err != nil {
			return fmt.Errorf("error copying file %s: %w", p, err)
		}
		return nil
	})
}
