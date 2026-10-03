//go:build linux

package utiles

import "syscall"

// diskFree 返回指定路径所在文件系统的可用字节数。
// 面板容器内 "/" 是 overlay 根，其底层就是宿主 /var/lib/docker 所在磁盘，
// 因此这里的数值可用于"快照会吃掉宿主系统盘多少空间"的量级判断。
func diskFree(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
