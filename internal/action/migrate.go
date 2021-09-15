package action

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/image"
)

type Migrate struct {
	IndexImage string
	OutputDir  string

	WriteFunc WriteFunc
	Registry  image.Registry
}

type WriteFunc func(config declcfg.DeclarativeConfig, w io.Writer) error

func (m Migrate) Run(ctx context.Context) error {
	entries, err := ioutil.ReadDir(m.OutputDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("output dir %q must be empty", m.OutputDir)
	}

	r := action.Render{
		Refs:           []string{m.IndexImage},
		AllowedRefMask: action.RefSqliteImage | action.RefSqliteFile,
	}
	if m.Registry != nil {
		r.Registry = m.Registry
	}

	fmt.Printf("rendering image %q as declarative config\n", m.IndexImage)
	cfg, err := r.Run(ctx)
	if err != nil {
		return fmt.Errorf("render index image: %v", err)
	}

	fmt.Printf("writing rendered declarative config to %q\n", m.OutputDir)
	return writeToFS(*cfg, m.OutputDir, m.WriteFunc)
}

const globalName = "__global"

func writeToFS(cfg declcfg.DeclarativeConfig, rootDir string, writeFunc WriteFunc) error {
	channelsByPackage := map[string][]declcfg.Channel{}
	for _, c := range cfg.Channels {
		channelsByPackage[c.Package] = append(channelsByPackage[c.Package], c)
	}
	bundlesByPackage := map[string][]declcfg.Bundle{}
	for _, b := range cfg.Bundles {
		bundlesByPackage[b.Package] = append(bundlesByPackage[b.Package], b)
	}
	othersByPackage := map[string][]declcfg.Meta{}
	for _, o := range cfg.Others {
		pkgName := o.Package
		if pkgName == "" {
			pkgName = globalName
		}
		othersByPackage[pkgName] = append(othersByPackage[pkgName], o)
	}

	if err := os.MkdirAll(rootDir, 0777); err != nil {
		return fmt.Errorf("mkdir %q: %v", rootDir, err)
	}

	for _, p := range cfg.Packages {
		fcfg := declcfg.DeclarativeConfig{
			Packages: []declcfg.Package{p},
			Channels: channelsByPackage[p.Name],
			Bundles:  bundlesByPackage[p.Name],
			Others:   othersByPackage[p.Name],
		}
		pkgDir := filepath.Join(rootDir, p.Name)
		if err := os.RemoveAll(pkgDir); err != nil {
			return err
		}
		if err := os.MkdirAll(pkgDir, 0777); err != nil {
			return err
		}
		filename := filepath.Join(pkgDir, "catalog.yaml")
		if err := writeFile(fcfg, filename, writeFunc); err != nil {
			return err
		}
	}

	if globals, ok := othersByPackage[globalName]; ok {
		gcfg := declcfg.DeclarativeConfig{
			Others: globals,
		}
		filename := filepath.Join(rootDir, fmt.Sprintf("%s.yaml", globalName))
		if err := writeFile(gcfg, filename, writeFunc); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(cfg declcfg.DeclarativeConfig, filename string, writeFunc WriteFunc) error {
	buf := &bytes.Buffer{}
	if err := writeFunc(cfg, buf); err != nil {
		return fmt.Errorf("write to buffer for %q: %v", filename, err)
	}
	if err := os.WriteFile(filename, buf.Bytes(), 0666); err != nil {
		return fmt.Errorf("write file %q: %v", filename, err)
	}
	return nil
}
