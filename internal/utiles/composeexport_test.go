package utiles

import (
	stdzip "archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	aeszip "github.com/yeka/zip"
)

// 打包器：明文走标准库可读；填密码走 AES-256，正确密码可解、错误密码被拒
func TestZipEntryWriterEncryption(t *testing.T) {
	content := "services:\n  app:\n    image: nginx:latest\n"

	// ① 明文
	buf := &bytes.Buffer{}
	w := newZipEntryWriter(buf, "")
	fw, err := w.CreateEntry("a/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(content))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := stdzip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("明文包应可被标准库读取: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("条目数不对: %d", len(zr.File))
	}

	// ② AES-256 加密
	buf2 := &bytes.Buffer{}
	w2 := newZipEntryWriter(buf2, "s3cret")
	fw2, err := w2.CreateEntry("a/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw2.Write([]byte(content))
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	// 标准库读加密包：文件名可见但内容读不了（属预期）
	if zr2, err2 := stdzip.NewReader(bytes.NewReader(buf2.Bytes()), int64(buf2.Len())); err2 == nil && len(zr2.File) > 0 {
		rc, rerr := zr2.File[0].Open()
		if rerr == nil {
			t.Fatalf("标准库不应能读出加密内容")
			_ = rc
		}
	}

	// 用 AES 库 + 正确密码解
	azr, err := aeszip.NewReader(bytes.NewReader(buf2.Bytes()), int64(buf2.Len()))
	if err != nil {
		t.Fatalf("AES 包应可被解析: %v", err)
	}
	azr.File[0].SetPassword("s3cret")
	f, err := azr.File[0].Open()
	if err != nil {
		t.Fatalf("正确密码应能打开: %v", err)
	}
	got, _ := io.ReadAll(f)
	f.Close()
	if string(got) != content {
		t.Fatalf("内容不一致: %q", got)
	}

	// 错误密码：打开或读取阶段应报错
	azr2, _ := aeszip.NewReader(bytes.NewReader(buf2.Bytes()), int64(buf2.Len()))
	azr2.File[0].SetPassword("wrong")
	f2, err2 := azr2.File[0].Open()
	if err2 == nil {
		_, rerr := io.ReadAll(f2)
		if rerr == nil {
			t.Fatalf("错误密码不应能读出内容")
		}
		f2.Close()
	}

	// 包本身是合法 zip（可被识别），文件名保留
	if !strings.Contains(string(buf2.Bytes()[:4]), "PK") {
		t.Fatalf("应是 zip 魔数")
	}
}
