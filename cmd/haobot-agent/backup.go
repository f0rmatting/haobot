package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/f0rmatting/haobot/internal/agent/store"
)

// recordingsDir holds call recordings inside the data directory.
const recordingsDir = "recordings"

// runBackup packs what moving to another machine needs: a consistent copy
// of the database, the key that unseals the saved API keys and SIM PINs,
// and the call recordings. The TLS certificate is left out, since it names
// this machine's addresses. It works while the agent runs.
func runBackup(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ExitOnError)
	dataDir := registerDataDir(flags)
	out := flags.String("o", "haobot-backup-"+time.Now().Format("20060102-150405")+".tar.gz", "备份文件")
	flags.Parse(args)

	if !exists(filepath.Join(*dataDir, dbName)) {
		return fmt.Errorf("%s 里没有数据库", *dataDir)
	}
	st, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	tmp, err := os.MkdirTemp("", "haobot-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	snapshot := filepath.Join(tmp, dbName)
	if err := st.BackupTo(context.Background(), snapshot); err != nil {
		return fmt.Errorf("复制数据库：%w", err)
	}

	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	files := 0
	add := func(name, src string) error {
		files++
		return addFile(tw, name, src)
	}
	err = add(dbName, snapshot)
	if err == nil && exists(filepath.Join(*dataDir, secretKeyName)) {
		err = add(secretKeyName, filepath.Join(*dataDir, secretKeyName))
	}
	if err == nil {
		err = filepath.WalkDir(filepath.Join(*dataDir, recordingsDir), func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			rel, _ := filepath.Rel(*dataDir, p)
			return add(filepath.ToSlash(rel), p)
		})
	}
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(*out)
		return err
	}
	fi, _ := os.Stat(*out)
	fmt.Printf("已备份 %d 个文件到 %s（%.1f MB）\n", files, *out, float64(fi.Size())/1e6)
	fmt.Println("备份里有加密密钥和通话录音，请妥善保管。在新机器上用 haobot-agent restore 恢复。")
	return nil
}

func addFile(tw *tar.Writer, name, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// restorable reports whether a backup entry is one runRestore writes:
// nothing outside the data directory and nothing it does not know.
func restorable(name string) bool {
	if name != path.Clean(name) || path.IsAbs(name) || strings.HasPrefix(name, "../") {
		return false
	}
	if name == dbName || name == secretKeyName {
		return true
	}
	// recordings/<device>/<file>, and nothing that climbs out of it.
	rest, ok := strings.CutPrefix(name, recordingsDir+"/")
	return ok && rest != "" && !slices.Contains(strings.Split(rest, "/"), "..")
}

// runRestore unpacks a backup into the data directory. The agent must not
// be running on it.
func runRestore(args []string) error {
	flags := flag.NewFlagSet("restore", flag.ExitOnError)
	dataDir := registerDataDir(flags)
	force := flags.Bool("force", false, "数据目录里已有数据库时仍然恢复（原数据库改名保留）")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: haobot-agent restore [-data 目录] [-force] 备份文件.tar.gz\n先停止 haobot-agent 再恢复。")
		flags.PrintDefaults()
	}
	flags.Parse(args)
	if flags.NArg() != 1 {
		flags.Usage()
		os.Exit(2)
	}
	db := filepath.Join(*dataDir, dbName)
	if exists(db) && !*force {
		return fmt.Errorf("%s 已经有数据库。确认要覆盖就加 -force（原数据库会改名保留）", *dataDir)
	}

	f, err := os.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("不是 haobot 备份文件：%w", err)
	}
	// Unpack beside the data first, so a broken backup changes nothing.
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(*dataDir, ".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	tr := tar.NewReader(gz)
	files, hasDB := 0, false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取备份：%w", err)
		}
		if h.Typeflag != tar.TypeReg || !restorable(h.Name) {
			fmt.Fprintf(os.Stderr, "跳过 %s\n", h.Name)
			continue
		}
		dst := filepath.Join(staging, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		os.Chtimes(dst, h.ModTime, h.ModTime)
		files++
		hasDB = hasDB || h.Name == dbName
	}
	if !hasDB {
		return errors.New("备份里没有数据库")
	}
	st, err := store.Open(filepath.Join(staging, dbName))
	if err != nil {
		return err
	}
	err = st.Check(context.Background())
	st.Close()
	if err != nil {
		return err
	}

	if exists(db) {
		old := db + ".before-restore-" + time.Now().Format("20060102-150405")
		for _, suffix := range []string{"-wal", "-shm", ""} {
			if err := os.Rename(db+suffix, old+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		fmt.Printf("原数据库已改名为 %s\n", old)
	}
	err = filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(staging, p)
		dst := filepath.Join(*dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.Rename(p, dst); err != nil {
			return err
		}
		// Run as root (sudo), the files go to whoever owns the data
		// directory, such as the service's user.
		return sameOwner(*dataDir, dst, rel)
	})
	if err != nil {
		return err
	}
	fmt.Printf("已恢复 %d 个文件到 %s，现在可以启动 haobot-agent。\n", files, *dataDir)
	return nil
}

// sameOwner gives path, and the directories leading to it from dir, the
// owner of dir; it only matters, and only works, when run as root.
func sameOwner(dir, path, rel string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid, gid := int(st.Uid), int(st.Gid)
	for p := path; ; p = filepath.Dir(p) {
		if err := os.Lchown(p, uid, gid); err != nil {
			return err
		}
		if rel = filepath.Dir(rel); rel == "." {
			return nil
		}
	}
}
