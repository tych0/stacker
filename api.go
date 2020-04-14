package stacker

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/anmitsu/go-shlex"
	"github.com/openSUSE/umoci/pkg/mtreefilter"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

const (
	MediaTypeImageBtrfsLayer  = "application/vnd.cisco.image.layer.btrfs"
	GitVersionAnnotation      = "ws.tycho.stacker.git_version"
	StackerContentsAnnotation = "ws.tycho.stacker.stacker_yaml"
)

var (
	// Stacker does a mkdir /stacker for bind mounting in imports and such.
	// Unfortunately, this causes the mtime on the directory to be changed,
	// and go-mtree picks that upas a diff and always generates it. Let's
	// mask this out. This of course prevents stuff like `chmod 0444 /` or
	// similar, but that's not a very common use case.
	LayerGenerationIgnoreRoot mtreefilter.FilterFunc = func(path string) bool {
		fmt.Printf("ignore root: %s\n", path)
		// the paths are supplied relative to the filter dir, so '.' is root.
		return path != "."
	}
)

// StackerConfig is a struct that contains global (or widely used) stacker
// config options.
type StackerConfig struct {
	StackerDir string `yaml:"stacker_dir"`
	OCIDir     string `yaml:"oci_dir"`
	RootFSDir  string `yaml:"rootfs_dir"`
}

// Substitutions - return an array of substitutions for StackerFiles
func (sc *StackerConfig) Substitutions() []string {
	return []string{
		fmt.Sprintf("STACKER_ROOTFS_DIR=%s", sc.RootFSDir),
		fmt.Sprintf("STACKER_STACKER_DIR=%s", sc.StackerDir),
		fmt.Sprintf("STACKER_OCI_DIR=%s", sc.OCIDir),
	}
}

type BuildConfig struct {
	Prerequisites []string `yaml:"prerequisites"`
}

type Stackerfile struct {
	// AfterSubstitutions is the contents of the stacker file after
	// substitutions (i.e., the content that is actually used by stacker).
	AfterSubstitutions string

	// internal is the actual representation of the stackerfile as a map.
	internal map[string]*Layer

	// fileOrder is the order of elements as they appear in the stackerfile.
	fileOrder []string

	// configuration specific for this specific build
	buildConfig *BuildConfig

	// path to stackerfile
	path string

	// directory relative to which the stackerfile content is referenced
	referenceDirectory string
}

func (sf *Stackerfile) Get(name string) (*Layer, bool) {
	// This is dumb, but if we do a direct return here, golang doesn't
	// resolve the "ok", and compilation fails.
	layer, ok := sf.internal[name]
	return layer, ok
}

func (sf *Stackerfile) Len() int {
	return len(sf.internal)
}

const (
	DockerType  = "docker"
	TarType     = "tar"
	OCIType     = "oci"
	BuiltType   = "built"
	ScratchType = "scratch"
	ZotType     = "zot"
)

// dockerishUrl represents a URL that looks like docker://image:tag; as of go
// 1.12.9 these are no longer parsed correctly via the url.Parse() function,
// since it complains about :tag not being a valid int (i.e. port number).
type dockerishUrl struct {
	Scheme string
	Host   string
	Tag    string
	Path   string
}

func newDockerishUrl(thing string) (dockerishUrl, error) {
	parts := strings.SplitN(thing, "://", 2)

	if len(parts) < 2 {
		return dockerishUrl{Path: thing}, nil
	}

	url := dockerishUrl{Scheme: parts[0]}
	pathSplit := strings.SplitN(parts[1], "/", 2)

	url.Host = pathSplit[0]
	if len(pathSplit) == 2 {
		url.Path = "/" + pathSplit[1]
	}

	tagSplit := strings.SplitN(url.Host, ":", 2)
	if len(tagSplit) == 2 {
		url.Tag = tagSplit[1]
	}

	return url, nil
}

type ImageSource struct {
	Type     string `yaml:"type"`
	Url      string `yaml:"url"`
	Tag      string `yaml:"tag"`
	Insecure bool   `yaml:"insecure"`
}

func NewImageSource(containersImageString string) (*ImageSource, error) {
	ret := &ImageSource{}
	if strings.HasPrefix(containersImageString, "oci:") {
		ret.Type = OCIType
		ret.Url = containersImageString[len("oci:"):]
		return ret, nil
	}

	url, err := newDockerishUrl(containersImageString)
	if err != nil {
		return nil, err
	}

	switch url.Scheme {
	case "docker":
		ret.Type = DockerType
		ret.Url = containersImageString
	case "zot":
		ret.Type = ZotType
		ret.Url = containersImageString
	default:
		return nil, errors.Errorf("unknown image source type: %s", containersImageString)
	}

	return ret, nil
}

// Returns a URL that can be passed to github.com/containers/image handling
// code.
func (is *ImageSource) ContainersImageURL() (string, error) {
	switch is.Type {
	case DockerType:
		return is.Url, nil
	case OCIType:
		return fmt.Sprintf("oci:%s", is.Url), nil
	case ZotType:
		return is.Url, nil
	default:
		return "", errors.Errorf("can't get containers/image url for source type: %s", is.Type)
	}
}

func (is *ImageSource) ParseTag() (string, error) {
	switch is.Type {
	case BuiltType:
		return is.Tag, nil
	case DockerType:
		url, err := newDockerishUrl(is.Url)
		if err != nil {
			return "", err
		}

		if url.Path != "" {
			return path.Base(strings.Split(url.Path, ":")[0]), nil
		}

		// skopeo allows docker://centos:latest or
		// docker://docker.io/centos:latest; if we don't have a
		// url path, let's use the host as the image tag
		return strings.Split(url.Host, ":")[0], nil
	case OCIType:
		pieces := strings.SplitN(is.Url, ":", 2)
		if len(pieces) != 2 {
			return "", fmt.Errorf("bad OCI tag: %s", is.Type)
		}

		return pieces[1], nil
	case ZotType:
		url, err := newDockerishUrl(is.Url)
		if err != nil {
			return "", err
		}

		if url.Path != "" {
			return path.Base(strings.Split(url.Path, ":")[0]), nil
		}

		return strings.Split(url.Host, ":")[0], nil
	default:
		return "", fmt.Errorf("unsupported type: %s", is.Type)
	}
}

type Layer struct {
	From               *ImageSource      `yaml:"from"`
	Import             interface{}       `yaml:"import"`
	Run                interface{}       `yaml:"run"`
	Cmd                interface{}       `yaml:"cmd"`
	Entrypoint         interface{}       `yaml:"entrypoint"`
	FullCommand        interface{}       `yaml:"full_command"`
	BuildEnvPt         []string          `yaml:"build_env_passthrough"`
	BuildEnv           map[string]string `yaml:"build_env"`
	Environment        map[string]string `yaml:"environment"`
	Volumes            []string          `yaml:"volumes"`
	Labels             map[string]string `yaml:"labels"`
	GenerateLabels     interface{}       `yaml:"generate_labels"`
	WorkingDir         string            `yaml:"working_dir"`
	BuildOnly          bool              `yaml:"build_only"`
	Binds              interface{}       `yaml:"binds"`
	Apply              []string          `yaml:"apply"`
	RuntimeUser        string            `yaml:"runtime_user"`
	referenceDirectory string            // Location of the directory where the layer is defined
}

func FilterEnv(matchList []string, curEnv map[string]string) (map[string]string, error) {
	// matchList is a list of regular expressions.
	// curEnv is a map[string]string.
	// return is filtered set of curEnv that match an entry in matchList
	var err error
	var r *regexp.Regexp
	newEnv := map[string]string{}
	matches := []*regexp.Regexp{}
	for _, t := range matchList {
		r, err = regexp.Compile("^" + t + "$")
		if err != nil {
			return newEnv, err
		}
		matches = append(matches, r)
	}
	for key, val := range curEnv {
		for _, match := range matches {
			if match.Match([]byte(key)) {
				newEnv[key] = val
				break
			}
		}
	}
	return newEnv, err
}

func buildEnv(passThrough []string, newEnv map[string]string,
	getCurEnv func() []string) (map[string]string, error) {
	// get a map[string]string that should be used for the environment
	// of the container.
	curEnv := map[string]string{}
	for _, kv := range getCurEnv() {
		pair := strings.SplitN(kv, "=", 2)
		curEnv[pair[0]] = pair[1]
	}
	defList := []string{
		"ftp_proxy", "http_proxy", "https_proxy", "no_proxy",
		"FTP_PROXY", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "TERM"}
	matchList := defList
	if len(passThrough) != 0 {
		matchList = passThrough
	}
	ret, err := FilterEnv(matchList, curEnv)
	if err != nil {
		return ret, err
	}
	for k, v := range newEnv {
		ret[k] = v
	}
	return ret, nil
}

func (l *Layer) BuildEnvironment() (map[string]string, error) {
	return buildEnv(l.BuildEnvPt, l.BuildEnv, os.Environ)
}

func (l *Layer) ParseCmd() ([]string, error) {
	return l.getStringOrStringSlice(l.Cmd, func(s string) ([]string, error) {
		return shlex.Split(s, true)
	})
}

func (l *Layer) ParseEntrypoint() ([]string, error) {
	return l.getStringOrStringSlice(l.Entrypoint, func(s string) ([]string, error) {
		return shlex.Split(s, true)
	})
}

func (l *Layer) ParseFullCommand() ([]string, error) {
	return l.getStringOrStringSlice(l.FullCommand, func(s string) ([]string, error) {
		return shlex.Split(s, true)
	})
}

func (l *Layer) ParseImport() ([]string, error) {
	rawImports, err := l.getStringOrStringSlice(l.Import, func(s string) ([]string, error) {
		return strings.Split(s, "\n"), nil
	})
	if err != nil {
		return nil, err
	}

	var absImports []string
	for _, rawImport := range rawImports {
		absImport, err := l.getAbsPath(rawImport)
		if err != nil {
			return nil, err
		}
		absImports = append(absImports, absImport)
	}
	return absImports, nil
}

func (l *Layer) ParseBinds() (map[string]string, error) {
	rawBinds, err := l.getStringOrStringSlice(l.Binds, func(s string) ([]string, error) {
		return []string{s}, nil
	})
	if err != nil {
		return nil, err
	}

	absBinds := make(map[string]string, len(rawBinds))
	for _, bind := range rawBinds {
		parts := strings.Split(bind, "->")
		if len(parts) != 1 && len(parts) != 2 {
			return nil, fmt.Errorf("invalid bind mount %s", bind)
		}

		source := strings.TrimSpace(parts[0])
		target := source

		absSource, err := l.getAbsPath(source)
		if err != nil {
			return nil, err
		}

		if len(parts) == 2 {
			target = strings.TrimSpace(parts[1])
		}

		absBinds[absSource] = target
	}

	return absBinds, nil

}

func (l *Layer) ParseRun() ([]string, error) {
	return l.getStringOrStringSlice(l.Run, func(s string) ([]string, error) {
		return []string{s}, nil
	})
}

func (l *Layer) ParseGenerateLabels() ([]string, error) {
	return l.getStringOrStringSlice(l.GenerateLabels, func(s string) ([]string, error) {
		return []string{s}, nil
	})
}

func (l *Layer) getAbsPath(path string) (string, error) {
	parsedPath, err := newDockerishUrl(path)
	if err != nil {
		return "", err
	}

	if parsedPath.Scheme != "" || filepath.IsAbs(path) {
		// Path is already absolute or is an URL, return it
		return path, nil
	} else {
		// If path is relative we need to add it to the directory where this layer is found
		return filepath.Abs(filepath.Join(l.referenceDirectory, path))
	}
}

func (l *Layer) getStringOrStringSlice(iface interface{}, xform func(string) ([]string, error)) ([]string, error) {
	// The user didn't supply run: at all, so let's not do anything.
	if iface == nil {
		return []string{}, nil
	}

	// This is how the json decoder decodes it if it's:
	// run:
	//     - foo
	//     - bar
	ifs, ok := iface.([]interface{})
	if ok {
		strs := []string{}
		for _, i := range ifs {
			s, ok := i.(string)
			if !ok {
				return nil, fmt.Errorf("unknown run array type: %T", i)
			}

			strs = append(strs, s)
		}
		return strs, nil
	}

	// This is how the json decoder decodes it if it's:
	// run: |
	//     echo hello world
	//     echo goodbye cruel world
	line, ok := iface.(string)
	if ok {
		return xform(line)
	}

	// This is how it is after we do our find replace and re-set it; as a
	// convenience (so we don't have to re-wrap it in interface{}), let's
	// handle []string
	strs, ok := iface.([]string)
	if ok {
		return strs, nil
	}

	return nil, fmt.Errorf("unknown directive type: %T", l.Run)
}

var (
	layerFields       []string
	imageSourceFields []string
)

func init() {
	layerFields = []string{}
	layerType := reflect.TypeOf(Layer{})
	for i := 0; i < layerType.NumField(); i++ {
		tag := layerType.Field(i).Tag.Get("yaml")
		layerFields = append(layerFields, tag)
	}

	imageSourceFields = []string{}
	imageSourceType := reflect.TypeOf(ImageSource{})
	for i := 0; i < imageSourceType.NumField(); i++ {
		tag := imageSourceType.Field(i).Tag.Get("yaml")
		imageSourceFields = append(imageSourceFields, tag)
	}
}

func substitute(content string, substitutions []string) (string, error) {
	for _, subst := range substitutions {
		membs := strings.SplitN(subst, "=", 2)
		if len(membs) != 2 {
			return "", fmt.Errorf("invalid substition %s", subst)
		}

		from := fmt.Sprintf("$%s", membs[0])
		to := membs[1]

		fmt.Fprintf(os.Stderr, "substituting %s to %s\n", from, to)

		content = strings.Replace(content, from, to, -1)

		re, err := regexp.Compile(fmt.Sprintf(`\$\{\{%s(:[^\}]*)?\}\}`, membs[0]))
		if err != nil {
			return "", err
		}

		content = re.ReplaceAllString(content, to)
	}

	// now, anything that's left we can just use its value
	re := regexp.MustCompile(`\$\{\{[^\}]*\}\}`)
	for {
		indexes := re.FindAllStringIndex(content, -1)
		if len(indexes) == 0 {
			break
		}

		idx := indexes[0]

		// get content without ${{}}
		variable := content[idx[0]+3 : idx[1]-2]

		membs := strings.SplitN(variable, ":", 2)
		if len(membs) != 2 {
			return "", fmt.Errorf("no value for substitution %s", variable)
		}

		buf := bytes.NewBufferString(content[:idx[0]])
		_, err := buf.WriteString(membs[1])
		if err != nil {
			return "", err
		}
		_, err = buf.WriteString(content[idx[1]:])
		if err != nil {
			return "", err
		}

		content = buf.String()
	}

	return content, nil
}

// NewStackerfile creates a new stackerfile from the given path. substitutions
// is a list of KEY=VALUE pairs of things to substitute. Note that this is
// explicitly not a map, because the substitutions are performed one at a time
// in the order that they are given.
func NewStackerfile(stackerfile string, substitutions []string) (*Stackerfile, error) {
	var err error

	sf := Stackerfile{}
	sf.path = stackerfile

	// Use working directory as default folder relative to which files
	// in the stacker yaml will be searched for
	sf.referenceDirectory, err = os.Getwd()
	if err != nil {
		return nil, err
	}

	url, err := newDockerishUrl(stackerfile)
	if err != nil {
		return nil, err
	}

	var raw []byte
	if url.Scheme == "" {
		raw, err = ioutil.ReadFile(stackerfile)
		if err != nil {
			return nil, err
		}

		// Make sure we use the absolute path to the Stackerfile
		sf.path, err = filepath.Abs(stackerfile)
		if err != nil {
			return nil, err
		}

		// This file is on the disk, use its parent directory
		sf.referenceDirectory = filepath.Dir(sf.path)

	} else {
		resp, err := http.Get(stackerfile)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("stackerfile: couldn't download %s: %s", stackerfile, resp.Status)
		}

		raw, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		// There's no need to update the reference directory of the stackerfile
		// Continue to use the working directory
	}

	content, err := substitute(string(raw), substitutions)
	if err != nil {
		return nil, err
	}

	sf.AfterSubstitutions = content

	// Parse the first time to validate the format/content
	ms := yaml.MapSlice{}
	if err := yaml.Unmarshal([]byte(content), &ms); err != nil {
		return nil, err
	}

	// Determine the layers in the stacker.yaml, their order and the list of prerequisite files
	sf.fileOrder = []string{}      // Order of layers
	sf.buildConfig = &BuildConfig{ // Stacker build configuration
		Prerequisites: []string{},
	}
	lms := yaml.MapSlice{} // Actual list of layers excluding the config directive
	for _, e := range ms {
		keyName, ok := e.Key.(string)
		if !ok {
			return nil, fmt.Errorf("stackerfile: cannot cast %v to string", e.Key)
		}

		if "config" == keyName {
			stackerConfigContent, err := yaml.Marshal(e.Value)
			if err != nil {
				return nil, err
			}
			if err = yaml.Unmarshal(stackerConfigContent, &sf.buildConfig); err != nil {
				msg := fmt.Sprintf("stackerfile: cannot interpret 'config' value, "+
					"note the 'config' section in the stackerfile cannot contain a layer definition %v", e.Value)
				return nil, errors.New(msg)
			}
		} else {
			sf.fileOrder = append(sf.fileOrder, e.Key.(string))
			lms = append(lms, e)
		}
	}

	// Now, let's make sure that all the things people supplied in the layers are
	// actually things this stacker understands.
	for _, e := range lms {
		for _, directive := range e.Value.(yaml.MapSlice) {
			found := false
			for _, field := range layerFields {
				if directive.Key.(string) == field {
					found = true
					break
				}
			}

			if !found {
				return nil, fmt.Errorf("stackerfile: unknown directive %s", directive.Key.(string))
			}

			if directive.Key.(string) == "from" {
				for _, sourceDirective := range directive.Value.(yaml.MapSlice) {
					found = false
					for _, field := range imageSourceFields {
						if sourceDirective.Key.(string) == field {
							found = true
							break
						}
					}

					if !found {
						return nil, fmt.Errorf("stackerfile: unknown image source directive %s",
							sourceDirective.Key.(string))
					}
				}
			}
		}
	}

	// Marshall only the layers so we can unmarshal them in the right data structure later
	layersContent, err := yaml.Marshal(lms)
	if err != nil {
		return nil, err
	}

	// Unmarshal to save the data in the right structure to enable further processing
	if err := yaml.Unmarshal(layersContent, &sf.internal); err != nil {
		return nil, err
	}

	for name, layer := range sf.internal {
		// Validate field values
		switch layer.From.Type {
		case BuiltType:
			if len(layer.From.Tag) == 0 {
				return nil, fmt.Errorf("%s: from tag cannot be empty for image type 'built'", name)
			}
		}

		// Set the directory with the location where the layer was defined
		layer.referenceDirectory = sf.referenceDirectory
	}

	return &sf, err
}

// DependencyOrder provides the list of layer names from a stackerfile
// the current order to be built, note this method does not reorder the layers,
// but it does validate they are specified in an order which makes sense
func (s *Stackerfile) DependencyOrder() ([]string, error) {
	ret := []string{}
	processed := map[string]bool{}
	// Determine if the stackerfile has other stackerfiles as dependencies
	hasPrerequisites := len(s.buildConfig.Prerequisites) > 0

	for i := 0; i < s.Len(); i++ {
		for _, name := range s.fileOrder {
			_, ok := processed[name]
			if ok {
				continue
			}

			layer := s.internal[name]

			if layer.From == nil {
				return nil, fmt.Errorf("invalid layer: no base (from directive)")
			}

			// Determine if the layer uses a previously processed layer as base
			_, baseTagProcessed := processed[layer.From.Tag]

			imports, err := layer.ParseImport()
			if err != nil {
				return nil, err
			}

			// Determine if the layer has stacker:// imports from another
			// layer which has not been processed
			allStackerImportsProcessed := true
			for _, imp := range imports {
				url, err := newDockerishUrl(imp)
				if err != nil {
					return nil, err
				}

				if url.Scheme != "stacker" {
					continue
				}

				_, ok := processed[url.Host]
				if !ok {
					allStackerImportsProcessed = false
					break
				}
			}

			if allStackerImportsProcessed && (layer.From.Type != BuiltType || baseTagProcessed) {
				// None of the imports using stacker:// are referencing unprocessed layers,
				// and in case the base layer is type build we have already processed it
				ret = append(ret, name)
				processed[name] = true
			} else if hasPrerequisites {
				// Just assume the imports are based on images defined in one of the stacker
				// files in the prerequisite paths
				ret = append(ret, name)
				processed[name] = true
			}
		}
	}

	if len(ret) != s.Len() {
		return nil, fmt.Errorf("couldn't resolve some dependencies")
	}

	return ret, nil
}

// Prerequisites provides the absolute paths to the Stackerfiles which are dependencies
// for building this Stackerfile
func (sf *Stackerfile) Prerequisites() ([]string, error) {
	// Cleanup paths in the prerequisites
	var prerequisitePaths []string
	for _, prerequisitePath := range sf.buildConfig.Prerequisites {
		parsedPath, err := newDockerishUrl(prerequisitePath)
		if err != nil {
			return nil, err
		}
		if parsedPath.Scheme != "" || filepath.IsAbs(prerequisitePath) {
			// Path is already absolute or is an URL, return it
			prerequisitePaths = append(prerequisitePaths, prerequisitePath)
		} else {
			// If path is relative we need to add it to the path to this stackerfile
			absPath, err := filepath.Abs(filepath.Join(sf.referenceDirectory, prerequisitePath))
			if err != nil {
				return nil, err
			}
			prerequisitePaths = append(prerequisitePaths, absPath)
		}
	}
	return prerequisitePaths, nil
}

// Logic for working with multiple StackerFiles
type StackerFiles map[string]*Stackerfile

// NewStackerFiles reads multiple Stackerfiles from a list of paths and applies substitutions
// It adds the Stackerfiles mentioned in the prerequisite paths to the results
func NewStackerFiles(paths []string, substituteVars []string) (StackerFiles, error) {
	sfm := make(map[string]*Stackerfile, len(paths))

	// Iterate over list of paths to stackerfiles
	for _, path := range paths {
		fmt.Printf("initializing stacker recipe: %s\n", path)

		// Read this stackerfile
		sf, err := NewStackerfile(path, substituteVars)
		if err != nil {
			return nil, err
		}

		// Add using absolute path to make sure the entries are unique
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if _, ok := sfm[absPath]; !ok {
			sfm[absPath] = sf
		}

		// Determine correct path of prerequisites
		prerequisites, err := sf.Prerequisites()
		if err != nil {
			return nil, err
		}

		// Need to also add stackerfile dependencies of this stackerfile to the map of stackerfiles
		depStackerFiles, err := NewStackerFiles(prerequisites, substituteVars)
		if err != nil {
			return nil, err
		}
		for depPath, depStackerFile := range depStackerFiles {
			sfm[depPath] = depStackerFile
		}
	}

	return sfm, nil
}

// LookupLayerDefinition searches for the Layer entry within the Stackerfiles
func (sfm StackerFiles) LookupLayerDefinition(name string) (*Layer, bool) {
	// Search for the layer in all of the stackerfiles
	for _, sf := range sfm {
		l, found := sf.Get(name)
		if found {
			return l, true
		}
	}
	return nil, false
}
