package backupCompose

import (
	composeType "github.com/compose-spec/compose-go/types"
)

type ComposeYaml struct {
	Services map[string]composeType.ServiceConfig `yaml:"services"`
}
