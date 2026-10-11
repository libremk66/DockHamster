package utiles

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/libremk66/DockHamster/internal/config"
	"github.com/libremk66/DockHamster/internal/svc"
)

// Compose 配置打包（导出下载 / WebDAV 备份共用）：
// 把「可读」项目的 compose 文件原样收进 zip（保留原文件名），并把同目录 .env 一并带上；
// 附一份 MANIFEST.txt 说明来源与跳过项。

// ComposeExportResult 打包结果摘要
type ComposeExportResult struct {
	Data     []byte // zip 内容
	Projects int    // 收录项目数
	Files    int    // 收录文件数
	Skipped  int    // 跳过项目数（不可读等）
}

// BuildComposeConfigZip onlyProject 为空 = 全部可读项目；否则只打包指定项目（不存在/不可读时报错）
func BuildComposeConfigZip(svcCtx *svc.ServiceContext, onlyProject string) (*ComposeExportResult, error) {
	// selfContainerID 传空：导出不需要"面板自身"信息（只影响挂载片段的 service 名）
	view, err := BuildComposeProjectsView(svcCtx, "")
	if err != nil {
		return nil, err
	}
	ts := time.Now().Format("20060102-150405")
	root := "compose-backup-" + ts + "/"

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	res := &ComposeExportResult{}
	var manifest strings.Builder
	fmt.Fprintf(&manifest, "DockHamster compose 配置备份\n生成时间：%s\n面板版本：%s\n\n", time.Now().Format("2006-01-02 15:04:05"), config.Version)

	matched := false
	projects := make([]ComposeProjectView, len(view.Projects))
	copy(projects, view.Projects)
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })

	for _, p := range projects {
		if onlyProject != "" && p.Name != onlyProject {
			continue
		}
		matched = true
		if p.State != "readable" || len(p.ResolvedFiles) == 0 {
			res.Skipped++
			fmt.Fprintf(&manifest, "[跳过] %s：%s\n", p.Name, stateReason(p.State))
			continue
		}
		added := 0
		seen := map[string]bool{}
		for _, f := range p.ResolvedFiles {
			if err := addFileToZip(zw, root+p.Name+"/"+filepath.Base(f), f); err != nil {
				fmt.Fprintf(&manifest, "[部分失败] %s：读取 %s 失败（%v）\n", p.Name, f, err)
				continue
			}
			seen[f] = true
			added++
		}
		// 同目录 .env（compose 变量插值的来源；存在就打进去）
		envPath := filepath.Join(filepath.Dir(p.ResolvedFiles[0]), ".env")
		if _, statErr := os.Stat(envPath); statErr == nil {
			if err := addFileToZip(zw, root+p.Name+"/.env", envPath); err == nil {
				added++
			}
		}
		if added == 0 {
			res.Skipped++
			fmt.Fprintf(&manifest, "[跳过] %s：文件读取失败\n", p.Name)
			continue
		}
		res.Projects++
		res.Files += added
		fmt.Fprintf(&manifest, "[收录] %s：%d 个文件（原始路径 %s）\n", p.Name, added, strings.Join(p.ConfigFiles, ", "))
	}
	if onlyProject != "" && !matched {
		return nil, fmt.Errorf("没有找到名为 %s 的项目", onlyProject)
	}
	if res.Projects == 0 {
		return nil, fmt.Errorf("没有可导出的配置：所有项目都读不到 compose 文件（先在「Compose」页按挂载引导挂载目录）")
	}

	// 清单入包
	w, werr := zw.Create(root + "MANIFEST.txt")
	if werr == nil {
		_, _ = w.Write([]byte(manifest.String()))
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	res.Data = buf.Bytes()
	return res, nil
}

func stateReason(state string) string {
	switch state {
	case "needsMount":
		return "面板读不到 compose 文件（需挂载，见「Compose」页挂载引导）"
	case "noFile":
		return "容器不是 compose 创建的（没有文件可导出）"
	default:
		return "不可读"
	}
}

func addFileToZip(zw *zip.Writer, name, srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
