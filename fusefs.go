package main

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/hanwen/go-fuse/v2/fuse/nodefs"
	"github.com/hanwen/go-fuse/v2/fuse/pathfs"
)

type profileHomeFS struct {
	pathfs.FileSystem
	shared pathfs.FileSystem
	auth   pathfs.FileSystem

	sharedHome string
	authFile   string
	authName   string
}

func mountProfileHome(mountpoint string, sharedHome string, authFile string) (*fuse.Server, error) {
	shared := pathfs.NewLoopbackFileSystem(sharedHome)
	auth := pathfs.NewLoopbackFileSystem(filepath.Dir(authFile))
	pfs := pathfs.NewLockingFileSystem(&profileHomeFS{
		FileSystem: pathfs.NewDefaultFileSystem(),
		shared:     shared,
		auth:       auth,
		sharedHome: sharedHome,
		authFile:   authFile,
		authName:   filepath.Base(authFile),
	})

	pathNode := pathfs.NewPathNodeFs(pfs, &pathfs.PathNodeFsOptions{})
	server, _, err := nodefs.Mount(mountpoint, pathNode.Root(), &fuse.MountOptions{
		FsName: sharedHome,
		Name:   "codex-profiled",
	}, nil)
	if err != nil {
		return nil, err
	}
	go server.Serve()
	return server, nil
}

func (p *profileHomeFS) String() string {
	return "codex-profiled"
}

func (p *profileHomeFS) SetDebug(debug bool) {
	p.shared.SetDebug(debug)
	p.auth.SetDebug(debug)
}

func (p *profileHomeFS) OnMount(nodeFs *pathfs.PathNodeFs) {
	p.shared.OnMount(nodeFs)
	p.auth.OnMount(nodeFs)
}

func (p *profileHomeFS) OnUnmount() {
	p.shared.OnUnmount()
	p.auth.OnUnmount()
}

func (p *profileHomeFS) GetAttr(name string, context *fuse.Context) (*fuse.Attr, fuse.Status) {
	return p.route(name).GetAttr(p.routeName(name), context)
}

func (p *profileHomeFS) Chmod(name string, mode uint32, context *fuse.Context) fuse.Status {
	return p.route(name).Chmod(p.routeName(name), mode, context)
}

func (p *profileHomeFS) Chown(name string, uid uint32, gid uint32, context *fuse.Context) fuse.Status {
	return p.route(name).Chown(p.routeName(name), uid, gid, context)
}

func (p *profileHomeFS) Utimens(name string, atime *time.Time, mtime *time.Time, context *fuse.Context) fuse.Status {
	return p.route(name).Utimens(p.routeName(name), atime, mtime, context)
}

func (p *profileHomeFS) Truncate(name string, size uint64, context *fuse.Context) fuse.Status {
	return p.route(name).Truncate(p.routeName(name), size, context)
}

func (p *profileHomeFS) Access(name string, mode uint32, context *fuse.Context) fuse.Status {
	return p.route(name).Access(p.routeName(name), mode, context)
}

func (p *profileHomeFS) Link(oldName string, newName string, context *fuse.Context) fuse.Status {
	if p.isAuth(oldName) || p.isAuth(newName) {
		if p.isAuth(oldName) && p.isAuth(newName) {
			return p.auth.Link(p.authName, p.authName, context)
		}
		return fuse.ToStatus(syscall.EXDEV)
	}
	return p.shared.Link(oldName, newName, context)
}

func (p *profileHomeFS) Mkdir(name string, mode uint32, context *fuse.Context) fuse.Status {
	if p.isAuth(name) {
		return fuse.ToStatus(syscall.ENOTDIR)
	}
	return p.shared.Mkdir(name, mode, context)
}

func (p *profileHomeFS) Mknod(name string, mode uint32, dev uint32, context *fuse.Context) fuse.Status {
	return p.route(name).Mknod(p.routeName(name), mode, dev, context)
}

func (p *profileHomeFS) Rename(oldName string, newName string, context *fuse.Context) fuse.Status {
	oldAuth := p.isAuth(oldName)
	newAuth := p.isAuth(newName)
	switch {
	case oldAuth && newAuth:
		return p.auth.Rename(p.authName, p.authName, context)
	case !oldAuth && !newAuth:
		return p.shared.Rename(oldName, newName, context)
	case !oldAuth && newAuth:
		return fuse.ToStatus(moveFile(filepath.Join(p.sharedHome, oldName), p.authFile))
	default:
		return fuse.ToStatus(syscall.EXDEV)
	}
}

func (p *profileHomeFS) Rmdir(name string, context *fuse.Context) fuse.Status {
	if p.isAuth(name) {
		return fuse.ToStatus(syscall.ENOTDIR)
	}
	return p.shared.Rmdir(name, context)
}

func (p *profileHomeFS) Unlink(name string, context *fuse.Context) fuse.Status {
	return p.route(name).Unlink(p.routeName(name), context)
}

func (p *profileHomeFS) GetXAttr(name string, attribute string, context *fuse.Context) ([]byte, fuse.Status) {
	return p.route(name).GetXAttr(p.routeName(name), attribute, context)
}

func (p *profileHomeFS) ListXAttr(name string, context *fuse.Context) ([]string, fuse.Status) {
	return p.route(name).ListXAttr(p.routeName(name), context)
}

func (p *profileHomeFS) RemoveXAttr(name string, attr string, context *fuse.Context) fuse.Status {
	return p.route(name).RemoveXAttr(p.routeName(name), attr, context)
}

func (p *profileHomeFS) SetXAttr(name string, attr string, data []byte, flags int, context *fuse.Context) fuse.Status {
	return p.route(name).SetXAttr(p.routeName(name), attr, data, flags, context)
}

func (p *profileHomeFS) Open(name string, flags uint32, context *fuse.Context) (nodefs.File, fuse.Status) {
	return p.route(name).Open(p.routeName(name), flags, context)
}

func (p *profileHomeFS) Create(name string, flags uint32, mode uint32, context *fuse.Context) (nodefs.File, fuse.Status) {
	return p.route(name).Create(p.routeName(name), flags, mode, context)
}

func (p *profileHomeFS) OpenDir(name string, context *fuse.Context) ([]fuse.DirEntry, fuse.Status) {
	entries, status := p.shared.OpenDir(name, context)
	if status != fuse.OK || name != "" {
		return entries, status
	}

	hasAuth := false
	for i := range entries {
		if entries[i].Name == "auth.json" {
			entries[i].Mode = fuse.S_IFREG
			hasAuth = true
			break
		}
	}
	if !hasAuth {
		if _, err := os.Stat(p.authFile); err == nil {
			entries = append(entries, fuse.DirEntry{Name: "auth.json", Mode: fuse.S_IFREG})
		}
	}
	return entries, fuse.OK
}

func (p *profileHomeFS) Symlink(value string, linkName string, context *fuse.Context) fuse.Status {
	return p.route(linkName).Symlink(value, p.routeName(linkName), context)
}

func (p *profileHomeFS) Readlink(name string, context *fuse.Context) (string, fuse.Status) {
	return p.route(name).Readlink(p.routeName(name), context)
}

func (p *profileHomeFS) StatFs(name string) *fuse.StatfsOut {
	return p.shared.StatFs(name)
}

func (p *profileHomeFS) route(name string) pathfs.FileSystem {
	if p.isAuth(name) {
		return p.auth
	}
	return p.shared
}

func (p *profileHomeFS) routeName(name string) string {
	if p.isAuth(name) {
		return p.authName
	}
	return name
}

func (p *profileHomeFS) isAuth(name string) bool {
	return name == "auth.json"
}

func moveFile(src string, dst string) syscall.Errno {
	if err := os.Rename(src, dst); err == nil {
		return 0
	} else if !isCrossDevice(err) {
		return errno(err)
	}

	in, err := os.Open(src)
	if err != nil {
		return errno(err)
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errno(err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return errno(copyErr)
	}
	if closeErr != nil {
		return errno(closeErr)
	}
	if err := os.Remove(src); err != nil {
		return errno(err)
	}
	return 0
}

func isCrossDevice(err error) bool {
	linkErr, ok := err.(*os.LinkError)
	return ok && linkErr.Err == syscall.EXDEV
}

func errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	if pathErr, ok := err.(*os.PathError); ok {
		return errno(pathErr.Err)
	}
	if linkErr, ok := err.(*os.LinkError); ok {
		return errno(linkErr.Err)
	}
	if errNo, ok := err.(syscall.Errno); ok {
		return errNo
	}
	return syscall.EIO
}
