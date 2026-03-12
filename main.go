package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"sort"

	"gopkg.in/yaml.v3"
)

var stripMetaFields = map[string]bool{
	"creationTimestamp": true,
	"resourceVersion":   true,
	"uid":               true,
	"generation":        true,
	"managedFields":     true,
}

var stripAnnotationPrefixes = []string{
	"meta.helm.sh/",
	"kubectl.kubernetes.io/",
	"deployment.kubernetes.io/",
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	namespace := parseNamespace(args)

	switch args[0] {
	case "list":
		if namespace == "" {
			fmt.Fprintln(os.Stderr, "Error: namespace required. Use -n <namespace>")
			os.Exit(1)
		}
		runList(namespace)
	default:
		if namespace == "" {
			fmt.Fprintln(os.Stderr, "Error: namespace required. Use -n <namespace>")
			os.Exit(1)
		}
		runGet(args[0], namespace)
	}
}

func parseNamespace(args []string) string {
	for i := 0; i < len(args); i++ {
		if (args[i] == "-n" || args[i] == "--namespace") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  ksd <secret-name> -n <namespace>   get & decode a secret")
	fmt.Fprintln(os.Stderr, "  ksd list -n <namespace>             list all secrets in namespace")
}

func kubectl(args ...string) ([]byte, error) {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH: %v", err)
	}
	out, err := exec.Command(path, args...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s", exitErr.Stderr)
		}
		return nil, err
	}
	return out, nil
}

func runList(namespace string) {
	out, err := kubectl("get", "secrets", "-n", namespace, "-o", "jsonpath={range .items[*]}{.metadata.name}{'\\n'}{end}")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(out))
}

func runGet(secretName, namespace string) {
	out, err := kubectl("get", "secret", secretName, "-n", namespace, "-o", "yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(out, &doc); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing YAML: %v\n", err)
		os.Exit(1)
	}

	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		processMapping(doc.Content[0], "")
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding YAML: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(buf.String())
}

func processMapping(node *yaml.Node, parentKey string) {
	if node.Kind != yaml.MappingNode {
		return
	}

	newContent := []*yaml.Node{}
	for i := 0; i < len(node.Content)-1; i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		key := keyNode.Value

		if parentKey == "metadata" && stripMetaFields[key] {
			continue
		}

		if parentKey == "metadata" && key == "annotations" {
			cleaned := cleanAnnotations(valNode)
			if cleaned == nil {
				continue
			}
			valNode = cleaned
		}

		if parentKey == "data" && valNode.Kind == yaml.ScalarNode {
			if decoded, err := base64.StdEncoding.DecodeString(valNode.Value); err == nil {
				valNode = &yaml.Node{Kind: yaml.ScalarNode, Value: string(decoded), Tag: "!!str"}
			}
		}

		if valNode.Kind == yaml.MappingNode {
			processMapping(valNode, key)
		}

		newContent = append(newContent, keyNode, valNode)
	}

	node.Content = newContent
}

func cleanAnnotations(node *yaml.Node) *yaml.Node {
	if node.Kind != yaml.MappingNode {
		return node
	}

	type kv struct{ k, v *yaml.Node }
	var pairs []kv
	for i := 0; i < len(node.Content)-1; i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if !isSystemAnnotation(k.Value) {
			pairs = append(pairs, kv{k, v})
		}
	}

	if len(pairs) == 0 {
		return nil
	}

	sort.Slice(pairs, func(i, j int) bool { return pairs[i].k.Value < pairs[j].k.Value })

	result := &yaml.Node{Kind: yaml.MappingNode, Tag: node.Tag}
	for _, p := range pairs {
		result.Content = append(result.Content, p.k, p.v)
	}
	return result
}

func isSystemAnnotation(key string) bool {
	for _, prefix := range stripAnnotationPrefixes {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}