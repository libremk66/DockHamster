package backupCompose

import (
	composeType "github.com/compose-spec/compose-go/types"
)

type composeYaml struct {
	Service map[string]composeType.ServiceConfig `yaml:"service"`
}
