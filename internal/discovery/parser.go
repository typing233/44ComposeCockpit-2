package discovery

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/composecockpit/server/internal/domain"
)

type Parser interface {
	Parse(ctx context.Context, project DiscoveredProject) (*domain.Project, error)
}

type composeParser struct {
	logger *slog.Logger
}

func NewParser(logger *slog.Logger) Parser {
	return &composeParser{logger: logger}
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Networks map[string]composeNetwork `yaml:"networks"`
	Volumes  map[string]composeVolume  `yaml:"volumes"`
	Include  []includeEntry            `yaml:"include"`
}

type composeService struct {
	Image       string                       `yaml:"image"`
	Build       interface{}                  `yaml:"build"`
	Ports       []interface{}                `yaml:"ports"`
	Environment interface{}                  `yaml:"environment"`
	Labels      interface{}                  `yaml:"labels"`
	DependsOn   interface{}                  `yaml:"depends_on"`
	Profiles    []string                     `yaml:"profiles"`
	Volumes     []interface{}                `yaml:"volumes"`
	Networks    interface{}                  `yaml:"networks"`
	Restart     string                       `yaml:"restart"`
	Command     interface{}                  `yaml:"command"`
	Entrypoint  interface{}                  `yaml:"entrypoint"`
	Extends     *extendsConfig               `yaml:"extends"`
}

type composeNetwork struct {
	Name     string            `yaml:"name"`
	Driver   string            `yaml:"driver"`
	External interface{}       `yaml:"external"`
	Labels   map[string]string `yaml:"labels"`
}

type composeVolume struct {
	Name     string            `yaml:"name"`
	Driver   string            `yaml:"driver"`
	External interface{}       `yaml:"external"`
	Labels   map[string]string `yaml:"labels"`
}

type includeEntry struct {
	Path    string `yaml:"path"`
	EnvFile string `yaml:"env_file"`
}

type extendsConfig struct {
	File    string `yaml:"file"`
	Service string `yaml:"service"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

func (p *composeParser) Parse(ctx context.Context, disc DiscoveredProject) (*domain.Project, error) {
	envVars := loadEnvFiles(disc.EnvFiles)
	for k, v := range getOSEnv() {
		if _, exists := envVars[k]; !exists {
			envVars[k] = v
		}
	}

	var merged composeFile
	merged.Services = make(map[string]composeService)
	merged.Networks = make(map[string]composeNetwork)
	merged.Volumes = make(map[string]composeVolume)

	for _, filePath := range disc.ComposeFiles {
		cf, err := p.parseFile(filePath, envVars)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", filePath, err)
		}
		p.mergeInto(&merged, cf)
	}

	for name, svc := range merged.Services {
		if svc.Extends != nil {
			resolved, err := p.resolveExtends(disc.Path, svc.Extends, envVars, 0)
			if err != nil {
				return nil, fmt.Errorf("resolve extends for service %s: %w", name, err)
			}
			merged.Services[name] = p.mergeService(resolved, svc)
		}
	}

	project := &domain.Project{
		ID:           GenerateProjectID(disc.Path),
		Name:         filepath.Base(disc.Path),
		Path:         disc.Path,
		ComposeFiles: disc.ComposeFiles,
		EnvFiles:     disc.EnvFiles,
		Services:     make([]domain.Service, 0, len(merged.Services)),
		Networks:     make(map[string]domain.Network),
		Volumes:      make(map[string]domain.Volume),
		Status:       domain.ProjectStatusUnknown,
	}

	profileSet := make(map[string]struct{})
	for name, svc := range merged.Services {
		domainSvc := p.convertService(name, svc, envVars)
		project.Services = append(project.Services, domainSvc)
		for _, prof := range domainSvc.Profiles {
			profileSet[prof] = struct{}{}
		}
	}

	for k := range profileSet {
		project.Profiles = append(project.Profiles, k)
	}

	for name, net := range merged.Networks {
		project.Networks[name] = domain.Network{
			Name:     net.Name,
			Driver:   net.Driver,
			External: parseBoolOrStruct(net.External),
			Labels:   net.Labels,
		}
	}

	for name, vol := range merged.Volumes {
		project.Volumes[name] = domain.Volume{
			Name:     vol.Name,
			Driver:   vol.Driver,
			External: parseBoolOrStruct(vol.External),
			Labels:   vol.Labels,
		}
	}

	return project, nil
}

func (p *composeParser) parseFile(filePath string, envVars map[string]string) (*composeFile, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	expanded := expandEnvVars(string(data), envVars)

	var cf composeFile
	if err := yaml.Unmarshal([]byte(expanded), &cf); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	return &cf, nil
}

func (p *composeParser) mergeInto(base, override *composeFile) {
	for name, svc := range override.Services {
		if existing, ok := base.Services[name]; ok {
			base.Services[name] = p.mergeService(existing, svc)
		} else {
			base.Services[name] = svc
		}
	}
	for name, net := range override.Networks {
		base.Networks[name] = net
	}
	for name, vol := range override.Volumes {
		base.Volumes[name] = vol
	}
}

func (p *composeParser) mergeService(base, override composeService) composeService {
	if override.Image != "" {
		base.Image = override.Image
	}
	if override.Build != nil {
		base.Build = override.Build
	}
	if override.Ports != nil {
		base.Ports = append(base.Ports, override.Ports...)
	}
	if override.Environment != nil {
		base.Environment = mergeEnvInterface(base.Environment, override.Environment)
	}
	if override.Labels != nil {
		base.Labels = mergeEnvInterface(base.Labels, override.Labels)
	}
	if override.DependsOn != nil {
		base.DependsOn = override.DependsOn
	}
	if override.Profiles != nil {
		base.Profiles = override.Profiles
	}
	if override.Volumes != nil {
		base.Volumes = override.Volumes
	}
	if override.Networks != nil {
		base.Networks = override.Networks
	}
	if override.Restart != "" {
		base.Restart = override.Restart
	}
	if override.Command != nil {
		base.Command = override.Command
	}
	if override.Entrypoint != nil {
		base.Entrypoint = override.Entrypoint
	}
	return base
}

func (p *composeParser) resolveExtends(baseDir string, ext *extendsConfig, envVars map[string]string, depth int) (composeService, error) {
	if depth > 10 {
		return composeService{}, fmt.Errorf("extends recursion limit exceeded")
	}

	filePath := filepath.Join(baseDir, ext.File)
	cf, err := p.parseFile(filePath, envVars)
	if err != nil {
		return composeService{}, fmt.Errorf("parse extends file %s: %w", filePath, err)
	}

	svc, ok := cf.Services[ext.Service]
	if !ok {
		return composeService{}, fmt.Errorf("service %q not found in %s", ext.Service, filePath)
	}

	if svc.Extends != nil {
		parentDir := filepath.Dir(filePath)
		parent, err := p.resolveExtends(parentDir, svc.Extends, envVars, depth+1)
		if err != nil {
			return composeService{}, err
		}
		svc = p.mergeService(parent, svc)
		svc.Extends = nil
	}

	return svc, nil
}

func (p *composeParser) convertService(name string, svc composeService, envVars map[string]string) domain.Service {
	ds := domain.Service{
		Name:        name,
		Image:       svc.Image,
		Profiles:    svc.Profiles,
		Restart:     svc.Restart,
		State:       domain.StateCreated,
		Health:      domain.HealthNone,
		Environment: parseEnvInterface(svc.Environment),
		Labels:      parseEnvInterface(svc.Labels),
	}

	if svc.Build != nil {
		ds.Build = parseBuildConfig(svc.Build)
	}

	ds.Ports = parsePorts(svc.Ports)
	ds.DependsOn = parseDependsOn(svc.DependsOn)
	ds.Volumes = parseVolumeMounts(svc.Volumes)
	ds.Networks = parseNetworksList(svc.Networks)
	ds.Command = parseStringOrSlice(svc.Command)
	ds.Entrypoint = parseStringOrSlice(svc.Entrypoint)

	return ds
}

func expandEnvVars(content string, envVars map[string]string) string {
	return envVarPattern.ReplaceAllStringFunc(content, func(match string) string {
		var varName, defaultVal string
		var hasDefault bool

		inner := match
		if strings.HasPrefix(inner, "${") {
			inner = inner[2 : len(inner)-1]
		} else {
			inner = inner[1:]
		}

		if idx := strings.Index(inner, ":-"); idx >= 0 {
			varName = inner[:idx]
			defaultVal = inner[idx+2:]
			hasDefault = true
		} else if idx := strings.Index(inner, "-"); idx >= 0 {
			varName = inner[:idx]
			defaultVal = inner[idx+1:]
			hasDefault = true
		} else {
			varName = inner
		}

		if val, ok := envVars[varName]; ok && val != "" {
			return val
		}
		if hasDefault {
			return defaultVal
		}
		return match
	})
}

func loadEnvFiles(files []string) map[string]string {
	env := make(map[string]string)
	for _, f := range files {
		parseEnvFile(f, env)
	}
	return env
}

func parseEnvFile(path string, env map[string]string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		env[key] = val
	}
}

func getOSEnv() map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return env
}

func parseEnvInterface(v interface{}) map[string]string {
	if v == nil {
		return nil
	}
	result := make(map[string]string)
	switch val := v.(type) {
	case map[string]string:
		return val
	case map[string]interface{}:
		for k, v := range val {
			result[k] = fmt.Sprintf("%v", v)
		}
	case []interface{}:
		for _, item := range val {
			s := fmt.Sprintf("%v", item)
			k, v, _ := strings.Cut(s, "=")
			result[k] = v
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func mergeEnvInterface(base, override interface{}) interface{} {
	baseMap := parseEnvInterface(base)
	overMap := parseEnvInterface(override)
	if baseMap == nil {
		baseMap = make(map[string]string)
	}
	for k, v := range overMap {
		baseMap[k] = v
	}
	return baseMap
}

func parseBuildConfig(v interface{}) *domain.BuildConfig {
	switch val := v.(type) {
	case string:
		return &domain.BuildConfig{Context: val}
	case map[string]interface{}:
		bc := &domain.BuildConfig{}
		if ctx, ok := val["context"].(string); ok {
			bc.Context = ctx
		}
		if df, ok := val["dockerfile"].(string); ok {
			bc.Dockerfile = df
		}
		if target, ok := val["target"].(string); ok {
			bc.Target = target
		}
		if args, ok := val["args"]; ok {
			bc.Args = parseEnvInterface(args)
		}
		return bc
	}
	return nil
}

func parsePorts(ports []interface{}) []domain.PortMapping {
	var result []domain.PortMapping
	for _, p := range ports {
		switch val := p.(type) {
		case string:
			pm := parsePortString(val)
			if pm != nil {
				result = append(result, *pm)
			}
		case int:
			result = append(result, domain.PortMapping{
				HostPort:      fmt.Sprintf("%d", val),
				ContainerPort: fmt.Sprintf("%d", val),
				Protocol:      "tcp",
			})
		case map[string]interface{}:
			pm := domain.PortMapping{Protocol: "tcp"}
			if target, ok := val["target"]; ok {
				pm.ContainerPort = fmt.Sprintf("%v", target)
			}
			if published, ok := val["published"]; ok {
				pm.HostPort = fmt.Sprintf("%v", published)
			}
			if proto, ok := val["protocol"].(string); ok {
				pm.Protocol = proto
			}
			if hostIP, ok := val["host_ip"].(string); ok {
				pm.HostIP = hostIP
			}
			result = append(result, pm)
		}
	}
	return result
}

func parsePortString(s string) *domain.PortMapping {
	pm := &domain.PortMapping{Protocol: "tcp"}

	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		pm.Protocol = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		pm.ContainerPort = parts[0]
		pm.HostPort = parts[0]
	case 2:
		pm.HostPort = parts[0]
		pm.ContainerPort = parts[1]
	case 3:
		pm.HostIP = parts[0]
		pm.HostPort = parts[1]
		pm.ContainerPort = parts[2]
	default:
		return nil
	}
	return pm
}

func parseDependsOn(v interface{}) map[string]domain.ServiceDependency {
	if v == nil {
		return nil
	}
	result := make(map[string]domain.ServiceDependency)
	switch val := v.(type) {
	case []interface{}:
		for _, item := range val {
			name := fmt.Sprintf("%v", item)
			result[name] = domain.ServiceDependency{Condition: "service_started"}
		}
	case map[string]interface{}:
		for name, cfg := range val {
			dep := domain.ServiceDependency{Condition: "service_started"}
			if cfgMap, ok := cfg.(map[string]interface{}); ok {
				if cond, ok := cfgMap["condition"].(string); ok {
					dep.Condition = cond
				}
			}
			result[name] = dep
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func parseVolumeMounts(vols []interface{}) []domain.VolumeMount {
	var result []domain.VolumeMount
	for _, v := range vols {
		switch val := v.(type) {
		case string:
			vm := parseVolumeString(val)
			result = append(result, vm)
		case map[string]interface{}:
			vm := domain.VolumeMount{Type: "volume"}
			if t, ok := val["type"].(string); ok {
				vm.Type = t
			}
			if src, ok := val["source"].(string); ok {
				vm.Source = src
			}
			if tgt, ok := val["target"].(string); ok {
				vm.Target = tgt
			}
			if ro, ok := val["read_only"].(bool); ok {
				vm.ReadOnly = ro
			}
			result = append(result, vm)
		}
	}
	return result
}

func parseVolumeString(s string) domain.VolumeMount {
	vm := domain.VolumeMount{Type: "volume"}
	parts := strings.SplitN(s, ":", 3)
	switch len(parts) {
	case 1:
		vm.Target = parts[0]
	case 2:
		vm.Source = parts[0]
		vm.Target = parts[1]
	case 3:
		vm.Source = parts[0]
		vm.Target = parts[1]
		if strings.Contains(parts[2], "ro") {
			vm.ReadOnly = true
		}
	}
	if strings.HasPrefix(vm.Source, "/") || strings.HasPrefix(vm.Source, "./") || strings.HasPrefix(vm.Source, "~") {
		vm.Type = "bind"
	}
	return vm
}

func parseNetworksList(v interface{}) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []interface{}:
		var nets []string
		for _, n := range val {
			nets = append(nets, fmt.Sprintf("%v", n))
		}
		return nets
	case map[string]interface{}:
		var nets []string
		for name := range val {
			nets = append(nets, name)
		}
		return nets
	}
	return nil
}

func parseStringOrSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case string:
		return strings.Fields(val)
	case []interface{}:
		var result []string
		for _, item := range val {
			result = append(result, fmt.Sprintf("%v", item))
		}
		return result
	}
	return nil
}

func parseBoolOrStruct(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case map[string]interface{}:
		return true
	}
	return false
}
