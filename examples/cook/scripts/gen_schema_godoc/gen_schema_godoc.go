package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/invopop/jsonschema"
	"golang.org/x/mod/modfile"
)

var schemaDir = "./schema"
var outputFile = filepath.Join(schemaDir, "godoc.json")

func main() {
	// Find schema directory and cd to its parent if needed
	absSchemaDir, err := findSchemaDir()
	if err != nil {
		panic(err)
	}
	parentDir := filepath.Dir(absSchemaDir)
	if err := os.Chdir(parentDir); err != nil {
		panic(err)
	}

	pkg, err := getPkgFromGoMod(parentDir)
	if err != nil {
		panic(err)
	}

	reflector := new(jsonschema.Reflector)
	if err := reflector.AddGoComments(pkg, schemaDir, jsonschema.WithFullComment()); err != nil {
		panic(fmt.Errorf("failed to add Go comments: %w", err))
	}

	jsonData, err := json.MarshalIndent(reflector.CommentMap, "", "  ")
	if err != nil {
		panic(fmt.Errorf("failed to marshal comment map: %w", err))
	}
	jsonData = append(jsonData, '\n')

	if err := os.WriteFile(outputFile, jsonData, 0644); err != nil {
		panic(fmt.Errorf("failed to write %s: %w", outputFile, err))
	}

	fmt.Printf("Generated %s (%d comments)\n", outputFile, len(reflector.CommentMap))
}

func findSchemaDir() (string, error) {
	candidates := []string{
		"./schema",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return filepath.Abs(c)
		}
	}

	// Fallback: search upward for a "bot/schema" or "schema" directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, "schema")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return filepath.Abs(candidate)
		}
		candidateBot := filepath.Join(dir, "bot", "schema")
		if info, err := os.Stat(candidateBot); err == nil && info.IsDir() {
			return filepath.Abs(candidateBot)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("schema directory not found")
}

func getPkgFromGoMod(dir string) (string, error) {
	goModDir, err := findGoModDir(dir)
	if err != nil {
		return "", err
	}

	goModPath := filepath.Join(goModDir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("failed to read go.mod: %w", err)
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return "", fmt.Errorf("failed to parse go.mod: %w", err)
	}

	modulePath := f.Module.Mod.Path
	rel, err := filepath.Rel(goModDir, dir)
	if err != nil {
		return "", fmt.Errorf("failed to get relative path from go.mod: %w", err)
	}

	if rel == "." {
		return modulePath, nil
	}
	return path.Join(modulePath, filepath.ToSlash(rel)), nil
}

func findGoModDir(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
