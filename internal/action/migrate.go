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
	"github.com/operator-framework/operator-registry/alpha/property"
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
		Refs:      []string{m.IndexImage},
		AllowMask: action.RefSqliteImage,
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
	return writeToFS(*cfg, &diskWriter{}, m.OutputDir, m.WriteFunc)
}

const globalName = "__global"

type fsWriter interface {
	MkdirAll(path string, mode os.FileMode) error
	WriteFile(path string, data []byte, mode os.FileMode) error
}

var _ fsWriter = &diskWriter{}

type diskWriter struct{}

func (w diskWriter) MkdirAll(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}

func (w diskWriter) WriteFile(path string, data []byte, mode os.FileMode) error {
	return ioutil.WriteFile(path, data, mode)
}

func writeToFS(cfg declcfg.DeclarativeConfig, w fsWriter, rootDir string, writeFunc WriteFunc) error {
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

	if err := w.MkdirAll(rootDir, 0777); err != nil {
		return fmt.Errorf("mkdir %q: %v", rootDir, err)
	}

	for _, p := range cfg.Packages {
		fcfg := declcfg.DeclarativeConfig{
			Packages: []declcfg.Package{p},
			Bundles:  bundlesByPackage[p.Name],
			Others:   othersByPackage[p.Name],
		}
		pkgDir := filepath.Join(rootDir, p.Name)
		if err := w.MkdirAll(pkgDir, 0777); err != nil {
			return err
		}
		filename := filepath.Join(pkgDir, "index.yaml")
		if err := writeFile(fcfg, w, filename, writeFunc); err != nil {
			return err
		}

		for _, b := range fcfg.Bundles {
			if err := writeObjectFiles(b, w, pkgDir); err != nil {
				return fmt.Errorf("write object files for bundle %q: %v", b.Name, err)
			}
		}
	}

	if globals, ok := othersByPackage[globalName]; ok {
		gcfg := declcfg.DeclarativeConfig{
			Others: globals,
		}
		filename := filepath.Join(rootDir, fmt.Sprintf("%s.yaml", globalName))
		if err := writeFile(gcfg, w, filename, writeFunc); err != nil {
			return err
		}
	}
	return nil
}

func writeObjectFiles(b declcfg.Bundle, w fsWriter, baseDir string) error {
	props, err := property.Parse(b.Properties)
	if err != nil {
		return fmt.Errorf("parse properties: %v", err)
	}
	if len(props.BundleObjects) != len(b.Objects) {
		return fmt.Errorf("expected %d properties of type %q, found %d", len(b.Objects), property.TypeBundleObject, len(props.BundleObjects))
	}
	for i, p := range props.BundleObjects {
		if p.IsRef() {
			objPath := filepath.Join(baseDir, p.GetRef())
			objDir := filepath.Dir(objPath)
			if err := w.MkdirAll(objDir, 0777); err != nil {
				return fmt.Errorf("create directory %q for bundle object ref %q: %v", objDir, p.GetRef(), err)
			}
			if err := w.WriteFile(objPath, []byte(b.Objects[i]), 0666); err != nil {
				return fmt.Errorf("write bundle object for ref %q: %v", p.GetRef(), err)
			}
		}
	}
	return nil
}

func writeFile(cfg declcfg.DeclarativeConfig, w fsWriter, filename string, writeFunc WriteFunc) error {
	buf := &bytes.Buffer{}
	if err := writeFunc(cfg, buf); err != nil {
		return fmt.Errorf("write to buffer for %q: %v", filename, err)
	}
	if err := w.WriteFile(filename, buf.Bytes(), 0666); err != nil {
		return fmt.Errorf("write file %q: %v", filename, err)
	}
	return nil
}
