package dialog

import (
	"syscall"
	"unsafe"
)

var (
	shell32 = syscall.NewLazyDLL("shell32.dll")
	ole32   = syscall.NewLazyDLL("ole32.dll")

	shBrowseForFolderW  = shell32.NewProc("SHBrowseForFolderW")
	shGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	coInitializeEx = ole32.NewProc("CoInitializeEx")
	coUninitialize = ole32.NewProc("CoUninitialize")
	coTaskMemFree = ole32.NewProc("CoTaskMemFree")
)

type BROWSEINFO struct {
	HwndOwner      syscall.Handle
	PidlRoot       uintptr
	PszDisplayName *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	ITitle         *uint16
	AhAccess       [2]uintptr
}

const (
	BIF_RETURNONLYFSDIRS = 0x0001
	BIF_NEWDIALOGSTYLE   = 0x0040
	BIF_EDITBOX          = 0x0010
)

func CoInitialize() error {
	hr, _, _ := coInitializeEx.Call(0, 0x2)
	if hr != 0 {
		return syscall.Errno(hr)
	}
	return nil
}

func CoUninitialize() {
	coUninitialize.Call()
}

func SelectFolder(title string) (string, error) {
	if err := CoInitialize(); err != nil {
		return "", err
	}
	defer CoUninitialize()

	var t *uint16
	if len(title) > 0 {
		t = syscall.StringToUTF16Ptr(title)
	}

	displayName := new([260]uint16)
	bi := BROWSEINFO{
		HwndOwner:      0,
		PidlRoot:       0,
		PszDisplayName: &displayName[0],
		UlFlags:        BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE | BIF_EDITBOX,
		Lpfn:           0,
		LParam:         0,
		ITitle:         t,
	}

	pidl, _, _ := shBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer coTaskMemFree.Call(pidl)

	var pathBuf [260]uint16
	shGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf)))

	return syscall.UTF16ToString(pathBuf[:]), nil
}
