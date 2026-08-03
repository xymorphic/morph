package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/xymorphic/morph/internal/fileedit"
	"github.com/xymorphic/morph/pkg/str"
	"gopkg.in/yaml.v3"
)

var durationType = reflect.TypeOf(time.Duration(0))

type configPathStep struct {
	key string
}

// ConfigUpdate describes config update.
type ConfigUpdate struct {
	Path  string
	Value string
}

// ConfigValue describes config value.
type ConfigValue struct {
	Path  string
	Value string
}

// GetConfigValues returns config values.
func GetConfigValues(envPath string, configPath string, paths []string) ([]ConfigValue, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("config path is required")
	}

	cfg, err := Load(envPath, configPath)
	if err != nil {
		return nil, err
	}

	values := make([]ConfigValue, 0, len(paths))
	root := reflect.ValueOf(*cfg)
	for _, path := range paths {
		normalizedPath := NormalizeConfigPathAlias(path)
		value, err := getConfigPathValue(root, normalizedPath)
		if err != nil {
			return nil, err
		}

		formatted, err := configValueToString(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", normalizedPath, err)
		}
		values = append(values, ConfigValue{
			Path:  normalizedPath,
			Value: formatted,
		})
	}

	return values, nil
}

// SetConfigValue updates config value.
func SetConfigValue(envPath string, configPath string, path string, value string) (string, error) {
	updatedPaths, err := SetConfigValues(envPath, configPath, []ConfigUpdate{{Path: path, Value: value}})
	if err != nil {
		return "", err
	}
	if len(updatedPaths) == 0 {
		return "", nil
	}

	return updatedPaths[0], nil
}

// SetConfigValues updates config values.
func SetConfigValues(envPath string, configPath string, updates []ConfigUpdate) ([]string, error) {
	return setConfigValues(envPath, configPath, updates, (*Config).Validate, nil)
}

func SetConfigValuesRelaxed(envPath string, configPath string, updates []ConfigUpdate) ([]string, error) {
	return setConfigValues(envPath, configPath, updates, (*Config).ValidateRelaxed, nil)
}

func SetConfigValuesRelaxedIfUnchanged(
	envPath string,
	configPath string,
	updates []ConfigUpdate,
	snapshot fileedit.Snapshot,
) ([]string, error) {
	return setConfigValues(envPath, configPath, updates, (*Config).ValidateRelaxed, &snapshot)
}

func setConfigValues(
	envPath string,
	configPath string,
	updates []ConfigUpdate,
	validate func(*Config) error,
	expected *fileedit.Snapshot,
) ([]string, error) {
	configPathValue := str.String(configPath)
	configPath = configPathValue.Trim()
	if configPath == "" {
		return nil, fmt.Errorf("config path is required")
	}
	if len(updates) == 0 {
		return nil, fmt.Errorf("config path and value are required")
	}

	defaultData, err := NewDefaultConfig().ToYAML()
	if err != nil {
		return nil, err
	}
	var snapshot fileedit.Snapshot
	if expected != nil {
		if filepath.Clean(expected.Path) != filepath.Clean(configPath) {
			return nil, errors.New("config snapshot path does not match config path")
		}
		snapshot = *expected
	} else {
		snapshot, err = fileedit.ReadSnapshot(configPath, defaultData)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}
	node, err := loadConfigYAMLNodeData(snapshot.Data)
	if err != nil {
		return nil, err
	}

	updatedPaths := make([]string, 0, len(updates))
	for _, update := range updates {
		steps, valueType, err := resolveConfigPath(update.Path)
		if err != nil {
			return nil, err
		}

		valueNode, err := configValueToYAMLNode(update.Value, valueType)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", NormalizeConfigPathAlias(update.Path), err)
		}
		if err := setYAMLNodePath(node, steps, valueNode); err != nil {
			return nil, err
		}
		updatedPaths = append(updatedPaths, NormalizeConfigPathAlias(update.Path))
	}

	data, err := encodeYAMLNode(node)
	if err != nil {
		return nil, err
	}
	if err := validateConfigYAML(envPath, configPath, data, validate); err != nil {
		return nil, err
	}
	if _, err := fileedit.ReplaceIfUnchanged(snapshot, data); err != nil {
		return nil, err
	}

	return updatedPaths, nil
}

func loadConfigYAMLNodeData(data []byte) (*yaml.Node, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}
	if len(node.Content) == 0 {
		node.Kind = yaml.DocumentNode
		node.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}

	return &node, nil
}

func validateConfigYAML(
	envPath string,
	configPath string,
	data []byte,
	validate func(*Config) error,
) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(configPath), ".morph-config-edit-*.yaml")
	if err != nil {
		return fmt.Errorf("create validation config: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write validation config: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close validation config: %w", err)
	}

	cfg, err := Load(envPath, tempPath)
	if err != nil {
		return err
	}
	if validate == nil {
		validate = (*Config).Validate
	}
	if err := validate(cfg); err != nil {
		return err
	}
	return nil
}

func encodeYAMLNode(node *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	normalizeConfigYAMLStyle(node)
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(4)
	if err := encoder.Encode(node); err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("encode config file: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close config encoder: %w", err)
	}

	return buf.Bytes(), nil
}

func normalizeConfigYAMLStyle(node *yaml.Node) {
	if node == nil {
		return
	}

	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style = 0
	}
	for _, child := range node.Content {
		normalizeConfigYAMLStyle(child)
	}
}

func resolveConfigPath(path string) ([]configPathStep, reflect.Type, error) {
	path = NormalizeConfigPathAlias(path)
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("config path is required")
	}

	current := reflect.TypeOf(Config{})
	steps := make([]configPathStep, 0, len(parts))
	for _, part := range parts {
		partValue := str.String(part)
		part = partValue.Trim()
		if part == "" {
			return nil, nil, fmt.Errorf("invalid config path %q", path)
		}

		for current.Kind() == reflect.Pointer {
			current = current.Elem()
		}
		if current == durationType {
			return nil, nil, fmt.Errorf("config path %q has extra segment %q", path, part)
		}

		switch current.Kind() {
		case reflect.Struct:
			field, key, ok := findConfigField(current, part)
			if !ok {
				return nil, nil, fmt.Errorf("unknown config path %q", path)
			}
			steps = append(steps, configPathStep{key: key})
			current = field.Type
		case reflect.Map:
			if current.Key().Kind() != reflect.String {
				return nil, nil, fmt.Errorf("unsupported config map path %q", path)
			}
			steps = append(steps, configPathStep{key: part})
			current = current.Elem()
		default:
			return nil, nil, fmt.Errorf("config path %q has extra segment %q", path, part)
		}
	}

	return steps, current, nil
}

func getConfigPathValue(current reflect.Value, path string) (reflect.Value, error) {
	path = NormalizeConfigPathAlias(path)
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return reflect.Value{}, fmt.Errorf("config path is required")
	}

	for _, part := range parts {
		partValue2 := str.String(part)
		part = partValue2.Trim()
		if part == "" {
			return reflect.Value{}, fmt.Errorf("invalid config path %q", path)
		}

		for current.Kind() == reflect.Pointer {
			if current.IsNil() {
				return reflect.Value{}, nil
			}

			current = current.Elem()
		}
		if current.Type() == durationType {
			return reflect.Value{}, fmt.Errorf("config path %q has extra segment %q", path, part)
		}

		switch current.Kind() {
		case reflect.Struct:
			field, _, ok := findConfigField(current.Type(), part)
			if !ok {
				return reflect.Value{}, fmt.Errorf("unknown config path %q", path)
			}
			current = current.FieldByIndex(field.Index)
		case reflect.Map:
			if current.Type().Key().Kind() != reflect.String {
				return reflect.Value{}, fmt.Errorf("unsupported config map path %q", path)
			}
			next := current.MapIndex(reflect.ValueOf(part))
			if !next.IsValid() {
				return reflect.Value{}, fmt.Errorf("unknown config path %q", path)
			}
			current = next
		default:
			return reflect.Value{}, fmt.Errorf("config path %q has extra segment %q", path, part)
		}
	}

	return current, nil
}

// NormalizeConfigPathAlias normalizes config path alias.
func NormalizeConfigPathAlias(path string) string {
	pathValue := str.String(path)
	path = pathValue.Trim()
	switch strings.ToLower(path) {
	case "search.enablerank":
		return "search.enableRerank"
	}

	parts := strings.Split(path, ".")
	for index, part := range parts {
		partValue3 := str.String(part)
		if strings.EqualFold(partValue3.Trim(), "baseURL") {
			parts[index] = "baseUrl"
		}
	}

	return strings.Join(parts, ".")
}

func findConfigField(container reflect.Type, part string) (reflect.StructField, string, bool) {
	partValue4 := str.String(part)
	part = partValue4.Trim()
	for field := range container.Fields() {
		field := field
		key := getYAMLFieldName(field)
		if key == "" || key == "-" {
			continue
		}
		if strings.EqualFold(key, part) || strings.EqualFold(field.Name, part) {
			return field, key, true
		}
	}

	return reflect.StructField{}, "", false
}

func getYAMLFieldName(field reflect.StructField) string {
	getValue := str.String(field.Tag.Get("yaml"))
	name := getValue.Trim()
	if name == "" {
		return field.Name
	}
	if before, _, ok := strings.Cut(name, ","); ok {
		return before
	}

	return name
}

func configValueToString(value reflect.Value) (string, error) {
	if !value.IsValid() {
		return "null", nil
	}

	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "null", nil
		}

		value = value.Elem()
	}

	if value.Type() == durationType {
		return value.Interface().(time.Duration).String(), nil
	}

	switch value.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'f', -1, value.Type().Bits()), nil
	case reflect.String:
		return value.String(), nil
	default:
		if !value.CanInterface() {
			return "", fmt.Errorf("cannot read config value")
		}

		data, err := json.Marshal(value.Interface())
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func configValueToYAMLNode(value string, target reflect.Type) (*yaml.Node, error) {
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target == durationType {
		if _, err := time.ParseDuration(value); err != nil {
			return nil, err
		}

		return scalarYAMLNode("!!str", value), nil
	}

	switch target.Kind() {
	case reflect.Bool:
		parsed, err := parseConfigBool(value)
		if err != nil {
			return nil, err
		}

		return scalarYAMLNode("!!bool", strconv.FormatBool(parsed)), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(value, 10, target.Bits())
		if err != nil {
			return nil, err
		}

		return scalarYAMLNode("!!int", strconv.FormatInt(parsed, 10)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(value, 10, target.Bits())
		if err != nil {
			return nil, err
		}

		return scalarYAMLNode("!!int", strconv.FormatUint(parsed, 10)), nil
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(value, target.Bits())
		if err != nil {
			return nil, err
		}

		return scalarYAMLNode("!!float", strconv.FormatFloat(parsed, 'f', -1, target.Bits())), nil
	case reflect.String:
		return scalarYAMLNode("!!str", value), nil
	case reflect.Slice:
		return configSliceValueToYAMLNode(value, target)
	case reflect.Map, reflect.Struct:
		return configCompositeValueToYAMLNode(value, target)
	default:
		return nil, fmt.Errorf("unsupported config value type %s", target)
	}
}

func scalarYAMLNode(tag string, value string) *yaml.Node {
	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   tag,
		Value: value,
	}
}

func parseConfigBool(value string) (bool, error) {
	valueText := str.String(value)
	switch valueText.Normalized() {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected bool, got %q", valueText)
	}
}

func configSliceValueToYAMLNode(value string, target reflect.Type) (*yaml.Node, error) {
	var values []string
	value2 := str.String(value)
	if strings.HasPrefix(value2.Trim(), "[") {
		if err := yaml.Unmarshal([]byte(value), &values); err != nil {
			return nil, err
		}
	} else {
		for _, part := range strings.Split(value, ",") {
			partValue5 := str.String(part)
			part = partValue5.Trim()
			if part != "" {
				values = append(values, part)
			}
		}
	}

	typed := reflect.New(target).Interface()
	data, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, typed); err != nil {
		return nil, err
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}

	return node.Content[0], nil
}

func configCompositeValueToYAMLNode(value string, target reflect.Type) (*yaml.Node, error) {
	typed := reflect.New(target).Interface()
	if err := yaml.Unmarshal([]byte(value), typed); err != nil {
		return nil, err
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(value), &node); err != nil {
		return nil, err
	}
	if len(node.Content) == 0 {
		return nil, errors.New("expected YAML object value")
	}

	return node.Content[0], nil
}

func setYAMLNodePath(root *yaml.Node, steps []configPathStep, valueNode *yaml.Node) error {
	if root == nil {
		return fmt.Errorf("config document is required")
	}
	if root.Kind != yaml.DocumentNode {
		return fmt.Errorf("config document must be a YAML document")
	}

	if len(root.Content) == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}

	current := root.Content[0]
	for i, step := range steps {
		normalizeYAMLNodeMapping(current)
		if current.Kind != yaml.MappingNode {
			return fmt.Errorf("config path %q is not a mapping", step.key)
		}
		if i == len(steps)-1 {
			setMappingValue(current, step.key, valueNode)
			removeLegacyMappingAliases(current, step.key)
			return nil
		}

		next := getMappingValue(current, step.key)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setMappingValue(current, step.key, next)
		}
		current = next
	}

	return nil
}

func normalizeYAMLNodeMapping(node *yaml.Node) {
	if node == nil {
		return
	}

	if node.Kind == 0 || node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		node.Kind = yaml.MappingNode
		node.Tag = "!!map"
		node.Value = ""
		node.Content = nil
	}
}

func getMappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

func setMappingValue(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}

	node.Content = append(
		node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func removeLegacyMappingAliases(node *yaml.Node, key string) {
	if key != "baseUrl" {
		return
	}

	removeMappingValue(node, "baseURL")
}

func removeMappingValue(node *yaml.Node, key string) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return
		}
	}
}
