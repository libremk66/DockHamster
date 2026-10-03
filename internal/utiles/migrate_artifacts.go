package utiles

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dockerBackend "github.com/docker/docker/api/types/backend"
	"github.com/docker/go-connections/nat"
	"github.com/libremk66/DockHamster/internal/module"
)

// ── 迁移包内的人读/人用产物：compose.yaml / import.sh / README.txt ──────

// yamlQuote 生成安全的 YAML 双引号字符串
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", ``, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// buildComposeYaml 从清单生成可直接使用的 compose 文件
func buildComposeYaml(m module.Manifest) string {
	var b strings.Builder
	b.WriteString("# ═══════════════════════════════════════════════════════════\n")
	b.WriteString("#  DockHamster 迁移包 · compose 文件（可直接使用）\n")
	b.WriteString("#  用法：先导入镜像（docker load -i images/*.tar.gz 或 ./import.sh），\n")
	b.WriteString("#       再执行 docker compose -f compose.yaml up -d\n")
	b.WriteString("#  也可把本文件内容粘贴进 Portainer（Stacks → Add stack）\n")
	b.WriteString("# ═══════════════════════════════════════════════════════════\n")
	b.WriteString("services:\n")

	for _, c := range m.Containers {
		var cc dockerBackend.ContainerCreateConfig
		if err := json.Unmarshal(c.Create, &cc); err != nil || cc.Config == nil {
			fmt.Fprintf(&b, "  # ⚠️ %s 的配置无法解析，已跳过\n", c.Name)
			continue
		}
		cfg, hc := cc.Config, cc.HostConfig
		fmt.Fprintf(&b, "\n  %s:\n", composeServiceKey(c.Name))
		fmt.Fprintf(&b, "    image: %s\n", yamlQuote(cfg.Image))
		fmt.Fprintf(&b, "    container_name: %s\n", yamlQuote(c.Name))
		if hc != nil {
			if hc.RestartPolicy.Name != "" {
				fmt.Fprintf(&b, "    restart: %s\n", hc.RestartPolicy.Name)
			}
			if hc.Privileged {
				b.WriteString("    privileged: true\n")
			}
			if hc.NetworkMode != "" && !strings.HasPrefix(string(hc.NetworkMode), "container:") {
				fmt.Fprintf(&b, "    network_mode: %s\n", yamlQuote(string(hc.NetworkMode)))
			}
			if hc.NetworkMode != "host" && len(hc.PortBindings) > 0 {
				b.WriteString("    ports:\n")
				keys := make([]string, 0, len(hc.PortBindings))
				for p := range hc.PortBindings {
					keys = append(keys, string(p))
				}
				sort.Strings(keys)
				for _, p := range keys {
					target := strings.SplitN(p, "/", 2)[0]
					proto := ""
					if strings.HasSuffix(p, "/udp") {
						proto = "/udp"
					}
					for _, bind := range hc.PortBindings[nat.Port(p)] {
						host := bind.HostPort
						if bind.HostIP != "" && bind.HostIP != "0.0.0.0" {
							host = bind.HostIP + ":" + host
						}
						fmt.Fprintf(&b, "      - %s\n", yamlQuote(host+":"+target+proto))
					}
				}
			}
		}
		// 环境变量
		if len(cfg.Env) > 0 {
			b.WriteString("    environment:\n")
			for _, kv := range cfg.Env {
				i := strings.Index(kv, "=")
				if i <= 0 {
					continue
				}
				fmt.Fprintf(&b, "      %s: %s\n", yamlQuote(kv[:i]), yamlQuote(kv[i+1:]))
			}
		}
		// 挂载
		if len(c.Volumes) > 0 {
			b.WriteString("    volumes:\n")
			for _, v := range c.Volumes {
				entry := v.Source + ":" + v.Target
				if v.ReadOnly {
					entry += ":ro"
				}
				fmt.Fprintf(&b, "      - %s\n", yamlQuote(entry))
			}
		}
		// 命令 / 入口
		if len(cfg.Entrypoint) > 0 {
			fmt.Fprintf(&b, "    entrypoint: [%s]\n", quoteList(cfg.Entrypoint))
		}
		if len(cfg.Cmd) > 0 {
			fmt.Fprintf(&b, "    command: [%s]\n", quoteList(cfg.Cmd))
		}
		if cfg.Tty {
			b.WriteString("    tty: true\n")
		}
		if hc != nil && len(hc.Devices) > 0 {
			b.WriteString("    devices:\n")
			for _, d := range hc.Devices {
				fmt.Fprintf(&b, "      - %s\n", yamlQuote(d.PathOnHost+":"+d.PathInContainer))
			}
		}
		if hc != nil && len(hc.CapAdd) > 0 {
			b.WriteString("    cap_add:\n")
			for _, cap := range hc.CapAdd {
				fmt.Fprintf(&b, "      - %s\n", yamlQuote(cap))
			}
		}
	}
	return b.String()
}

func quoteList(items []string) string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, yamlQuote(s))
	}
	return strings.Join(out, ", ")
}

// composeServiceKey 生成合法且可读的 service 键
func composeServiceKey(name string) string {
	k := strings.ToLower(name)
	k = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, k)
	k = strings.Trim(k, "-")
	if k == "" {
		k = "service"
	}
	return k
}

// buildImportScript 生成"无面板导入脚本"（占位符替换，避免 fmt 与脚本内 % 冲突）
func buildImportScript(pkgName string) string {
	return strings.ReplaceAll(importScriptTemplate, "__PKG_NAME__", pkgName)
}

// buildReadmeTxt 生成人读指引（三种场景 + 卷路径清单）
func buildReadmeTxt(pkgName string, m module.Manifest) string {
	var volLines strings.Builder
	for _, c := range m.Containers {
		if len(c.Volumes) == 0 {
			volLines.WriteString(fmt.Sprintf("  %-22s （无挂载）\n", c.Name))
			continue
		}
		for _, v := range c.Volumes {
			note := ""
			if v.ReadOnly {
				note = "（只读）"
			}
			volLines.WriteString(fmt.Sprintf("  %-22s %s → 容器内 %s %s\n", c.Name, v.Source, v.Target, note))
		}
	}
	var imgLines strings.Builder
	for _, img := range m.Images {
		ref := "(无标签)"
		if len(img.Refs) > 0 {
			ref = img.Refs[0]
		}
		src := map[string]string{"registry": "公共可得", "local-build": "本地构建", "dangling": "悬空"}[img.Source]
		imgLines.WriteString(fmt.Sprintf("  %-42s %8s  %s\n", ref, humanBytes(img.Size), src))
	}
	out := strings.NewReplacer(
		"__PKG_NAME__", pkgName,
		"__CREATED_AT__", m.Generator.CreatedAt.Format("2006-01-02 15:04"),
		"__HOST__", m.Generator.Host,
		"__VERSION__", m.Generator.Version,
		"__CONTAINER_COUNT__", fmt.Sprint(len(m.Containers)),
		"__IMAGE_COUNT__", fmt.Sprint(len(m.Images)),
		"__VOLUME_LIST__", volLines.String(),
		"__IMAGE_LIST__", imgLines.String(),
	).Replace(readmeTemplate)
	return out
}
