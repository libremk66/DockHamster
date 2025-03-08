package backupCompose

import (
	composeType "github.com/compose-spec/compose-go/types"
	dockerTypes "github.com/docker/docker/api/types"
	composeNat "github.com/docker/go-connections/nat"
	"github.com/zeromicro/go-zero/core/logx"
	"os"
	"path/filepath"
	"sigs.k8s.io/yaml"
	"strconv"
	"strings"
	"time"
)

// DockerConfig2ComposeYaml 将docker config转换为docker-compose.yaml
func DockerConfig2ComposeYaml(containerJSONs []dockerTypes.ContainerJSON) (err error) {
	var c composeYaml
	for _, containerJSON := range containerJSONs {
		var s composeType.ServiceConfig
		formatBaseServiceConfig(containerJSON, &s)
		formatEnvServiceConfig(containerJSON, &s)
		formatNetworkServiceConfig(containerJSON, &s)
		formatVolumeServiceConfig(containerJSON, &s)
		c.Service[containerJSON.Name] = s
	}
	// write to file
	backupDir := os.Getenv("BACKUP_DIR") // 从环境变量中获取备份目录
	if backupDir == "" {
		backupDir = "/data/backups" // 如果环境变量未设置，使用默认值
	}
	_, err = os.Stat(backupDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(backupDir, 0755)
		if err != nil {
			logx.Error("Error creating backup directory:", err)
			return err
		}
	}
	yamlData, yamlMarshalErr := yaml.Marshal(c)
	if yamlMarshalErr != nil {
		logx.Errorf("Error marshalling data err is: %v", yamlMarshalErr)
	}
	currentDate := time.Now().Format("2006-01-02")
	fileName := "backup-" + currentDate + ".yaml"
	fullPath := filepath.Join(backupDir, fileName)
	err = os.WriteFile(fullPath, yamlData, 0644)
	if err != nil {
		logx.Error("Error writing to file:", err)
		return err
	}
	return
}

func formatBaseServiceConfig(containerJSON dockerTypes.ContainerJSON, s *composeType.ServiceConfig) {
	s.Image = containerJSON.Config.Image
	name, cutNameResult := strings.CutPrefix(containerJSON.Name, "/")
	if !cutNameResult {
		logx.Infof("cutting name is: %s", containerJSON.Name)
	}
	s.ContainerName = name
	s.Name = containerJSON.Name
	s.Tty = containerJSON.Config.Tty
	entrypointErr := s.Entrypoint.DecodeMapstructure(containerJSON.Config.Entrypoint)
	if entrypointErr != nil {
		logx.Errorf("Error decoding entrypoint err is: %v", entrypointErr)
	}
	cmdErr := s.Command.DecodeMapstructure(containerJSON.Config.Cmd)
	if cmdErr != nil {
		logx.Errorf("Error decoding cmd err is: %v", cmdErr)
	}
	s.WorkingDir = containerJSON.Config.WorkingDir
	s.Restart = string(containerJSON.HostConfig.RestartPolicy.Name)
	s.Privileged = containerJSON.HostConfig.Privileged
	return
}

func formatEnvServiceConfig(containerJSON dockerTypes.ContainerJSON, s *composeType.ServiceConfig) {
	s.Environment = composeType.NewMappingWithEquals(containerJSON.Config.Env)
	return
}

func formatNetworkServiceConfig(containerJSON dockerTypes.ContainerJSON, s *composeType.ServiceConfig) {
	s.NetworkMode = string(containerJSON.HostConfig.NetworkMode)
	for containerPort, v := range containerJSON.HostConfig.PortBindings {
		var p composeType.ServicePortConfig
		port, proto := composeNat.SplitProtoPort(string(containerPort))
		portNum, convertErr := strconv.Atoi(port)
		if convertErr != nil {
			logx.Errorf("Error converting port err is: %v", convertErr)
			continue
		}
		p.Target = uint32(portNum)
		p.Published = v[0].HostPort
		p.Protocol = proto
		s.Ports = append(s.Ports, p)
	}
}

func formatVolumeServiceConfig(containerJSON dockerTypes.ContainerJSON, s *composeType.ServiceConfig) {
	for _, containerVolume := range containerJSON.Mounts {
		var v composeType.ServiceVolumeConfig
		v.Type = string(containerVolume.Type)
		v.Source = containerVolume.Source
		v.Target = containerVolume.Destination
		v.ReadOnly = containerVolume.RW
		s.Volumes = append(s.Volumes, v)
	}
}
