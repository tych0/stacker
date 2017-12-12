package stacker

import (
	"fmt"
	"io/ioutil"

	"gopkg.in/yaml.v2"
	"github.com/anmitsu/go-shlex"
)

// StackerConfig is a struct that contains global (or widely used) stacker
// config options.
type StackerConfig struct {
	StackerDir string
	OCIDir     string
	RootFSDir  string
}

type Stackerfile map[string]*Layer

const (
	DockerType = "docker"
	TarType    = "tar"
	OCIType    = "oci"
	BuiltType  = "built"
)

type ImageSource struct {
	Type string `yaml:"type"`
	Url  string `yaml:"url"`
	Tag  string `yaml:"tag"`
	Path string `yaml:"path"`
}

type Layer struct {
	From       *ImageSource `yaml:"from"`
	Import     []string     `yaml:"import"`
	Run        []string     `yaml:"run"`
	Entrypoint string       `yaml:"entrypoint"`
}

func (l *Layer) ParseEntrypoint() ([]string, error) {
	return shlex.Split(l.Entrypoint, true)
}

func NewStackerfile(stackerfile string) (Stackerfile, error) {
	sf := Stackerfile{}

	raw, err := ioutil.ReadFile(stackerfile)
	if err != nil {
		return nil, err
	}

	if err := yaml.Unmarshal(raw, &sf); err != nil {
		return nil, err
	}

	return sf, err
}

func (s *Stackerfile) DependencyOrder() ([]string, error) {
	ret := []string{}

	for i := 0; i < len(*s); i++ {
		for name, layer := range *s {
			have := false
			haveTag := false
			for _, l := range ret {
				if l == name {
					have = true
				}

				if l == layer.From.Tag {
					haveTag = true
				}
			}

			// do we have this layer yet?
			if !have {
				// all imported layers have no deps
				if layer.From.Type != BuiltType {
					ret = append(ret, name)
				}

				// otherwise, we need to have the tag
				if haveTag {
					ret = append(ret, name)
				}
			}
		}
	}

	if len(ret) != len(*s) {
		return nil, fmt.Errorf("couldn't resolve some dependencies")
	}

	return ret, nil
}
