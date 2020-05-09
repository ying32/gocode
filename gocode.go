package main

import "C"
import (
	"fmt"
	"go/build"
	"sync"
	"unsafe"
)

var (
	g_debug       = new(bool)
	servicesCount = 0
	rwLock        sync.Mutex
)

func main() {

}

func newDaemon() *daemon {
	d := new(daemon)
	defer func() {
		if recover() != nil {
			d = nil
		}
	}()

	d.pkgcache = new_package_cache()
	d.declcache = new_decl_cache(&d.context)
	d.autocomplete = new_auto_complete_context(d.pkgcache, d.declcache)
	//g_config.read()

	return d
}

//export startServer
func startServer() bool {

	if g_daemon == nil {
		//	*g_debug = true
		g_daemon = newDaemon()
	}
	rwLock.Lock()
	defer rwLock.Unlock()
	if g_daemon != nil {
		servicesCount++
	}
	fmt.Println("startServer: ", servicesCount)
	return g_daemon != nil
}

//export closeServer
func closeServer() {
	rwLock.Lock()
	defer rwLock.Unlock()
	servicesCount--
	if servicesCount <= 0 {
		g_daemon = nil
		servicesCount = 0
	}
}

//export setServiceOptions
func setServiceOptions(aKey uintptr, aKeyLen int, aValue uintptr, aValueLen int) {
	server_set(copyStr(aKey, aKeyLen), copyStr(aValue, aValueLen))
}

func copyStr(src uintptr, strlen int) string {
	if strlen == 0 {
		return ""
	}
	str := make([]uint8, strlen)
	for i := 0; i < strlen; i++ {
		str[i] = *(*uint8)(unsafe.Pointer(src + uintptr(i)))
	}
	return string(str)
}

//export serverAutoComplete
func serverAutoComplete(aData uintptr, dataLen int, aFilename uintptr, fileNameLen int, cursor int, outObj uintptr, writeProc uintptr) int {
	if g_daemon == nil {
		return -1
	}

	file := []byte(copyStr(aData, dataLen))
	filename := copyStr(aFilename, fileNameLen)

	context := pack_build_context(&build.Default)

	candidates, n := server_auto_complete(file, filename, cursor, context)
	//buffer := bytes.NewBuffer(nil)
	for _, c := range candidates {
		writeData(writeProc, outObj, fmt.Sprintf("%s,,%s,,%s\000", c.Class, c.Name, c.Type))
		//buffer.WriteString()
	}

	//fmt.Println(buffer.String())

	return n
}
