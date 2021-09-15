package action

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/blang/semver"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/alpha/property"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Add struct {
	FromDir      string
	BundleImages []string

	OverwriteLatest bool
	Log             *logrus.Logger
}

func (a Add) Run(ctx context.Context) error {
	if err := ensureDir(a.FromDir); err != nil {
		return fmt.Errorf("ensure root declarative config directory %q: %v", a.FromDir, err)
	}

	fbc, err := declcfg.LoadFS(os.DirFS(a.FromDir))
	if err != nil {
		return fmt.Errorf("load file-based catalog at %q: %v", a.FromDir, err)
	}
	m, err := declcfg.ConvertToModel(*fbc)
	if err != nil {
		return fmt.Errorf("input file-based catalog at %q is invalid: %s", a.FromDir, err)
	}

	reg, err := newRegistry()
	if err != nil {
		return fmt.Errorf("create temporary image registry: %v", err)
	}
	defer destroyRegistry(reg, a.Log)

	bundlesMap, err := a.loadBundles(ctx, reg, a.BundleImages)
	if err != nil {
		return fmt.Errorf("load bundles: %v", err)
	}
	for packageName, bundles := range bundlesMap {
		existingImages := sets.NewString()
		bundleChannels := map[string][]string{}
		pkg, ok := m[packageName]
		if ok {
			for _, ch := range pkg.Channels {
				for _, b := range ch.Bundles {
					bundleChannels[b.Name] = append(bundleChannels[b.Name], ch.Name)
				}
			}

			for _, ch := range pkg.Channels {
				for _, b := range ch.Bundles {
					existingImages.Insert(b.Image)
				}
			}
		}
		pkgBundles, err := a.loadBundles(ctx, reg, existingImages.List())
		if err != nil {
			return fmt.Errorf("load images from existing bundles in package %q: %v", packageName, err)
		}
		packageBundles := map[string]*bundle{}
		existingBundles := []*registry.Bundle{}
		for _, b := range pkgBundles[packageName] {
			b := b
			b.Channels = bundleChannels[b.Name]
			b.Annotations.Channels = strings.Join(b.Channels, ",")
			packageBundles[b.Name] = &b
			existingBundles = append(existingBundles, &b.Bundle)
		}
		nonChannelHeads := sets.NewString()
		if len(existingBundles) > 0 {
			existingPackageManifest, err := registry.SemverPackageManifest(existingBundles)
			if err != nil {
				return fmt.Errorf("get existing package manifest for package %q: %v", packageName, err)
			}
			for _, ch := range existingPackageManifest.Channels {
				for _, b := range pkg.Channels[ch.Name].Bundles {
					if b.Name != ch.CurrentCSVName {
						nonChannelHeads.Insert(b.Name)
					}
				}
			}
		}

		if a.OverwriteLatest {
			// make sure none of the bundles we're adding are found in the
			// existing channels as non-channel-heads.
			for _, b := range bundles {
				if nonChannelHeads.Has(b.Name) {
					return fmt.Errorf("cannot overwrite bundle %q: it is not exclusively a channel head", b.Name)
				}
			}
		} else {
			for _, b := range bundles {
				if _, ok := packageBundles[b.Name]; ok {
					return fmt.Errorf("bundle %q already present in package", b.Name)
				}
			}
		}

		for _, b := range bundles {
			b := b
			packageBundles[b.Name] = &b
		}
		newRegistryBundles := []*registry.Bundle{}
		subBundles := []*bundle{}
		for _, b := range packageBundles {
			if b.SubstitutesFor == "" {
				newRegistryBundles = append(newRegistryBundles, &b.Bundle)
			} else {
				subBundles = append(subBundles, b)
			}
		}
		newPackageManifest, err := registry.SemverPackageManifest(newRegistryBundles)
		if err != nil {
			return fmt.Errorf("get existing package manifest for package %q: %v", packageName, err)
		}
		defChHeadName := ""
		for _, ch := range newPackageManifest.Channels {
			if ch.Name == newPackageManifest.GetDefaultChannel() {
				defChHeadName = ch.CurrentCSVName
				break
			}
		}
		icon := packageBundles[defChHeadName].Icon
		pkg = &model.Package{
			Name:        packageName,
			Description: packageBundles[defChHeadName].Description,
			Icon: &model.Icon{
				Data:      icon.Data,
				MediaType: icon.MediaType,
			},
			Channels: map[string]*model.Channel{},
		}
		for _, ch := range newPackageManifest.Channels {
			mch := &model.Channel{
				Package: pkg,
				Name:    ch.Name,
				Bundles: map[string]*model.Bundle{},
			}
			cur := packageBundles[ch.CurrentCSVName]
			for cur != nil {
				mch.Bundles[cur.Name] = cur.ToModel(pkg, mch)
				cur = packageBundles[cur.Replaces]
			}
			pkg.Channels[ch.Name] = mch
			if newPackageManifest.DefaultChannelName == mch.Name {
				pkg.DefaultChannel = mch
			}
		}

		for _, ch := range pkg.Channels {
			head, err := ch.Head()
			if err != nil {
				return fmt.Errorf("get head of channel %q in package %q: %v", ch.Name, packageName, err)
			}
			for _, b := range ch.Bundles {
				if b != head {
					tmpProperties := b.Properties[:0]
					for _, p := range b.Properties {
						if p.Type != property.TypeBundleObject {
							tmpProperties = append(tmpProperties, p)
						}
					}
					b.Properties = tmpProperties
				}
			}
		}

		newM := model.Model{packageName: pkg}
		if err := newM.Validate(); err != nil {
			return fmt.Errorf("updated package %q is invalid: %v", packageName, err)
		}
		pkgOut := declcfg.ConvertFromModel(newM)

		if len(subBundles) > 0 {
			originals, chains, err := getSubsChains(subBundles)
			if err != nil {
				return fmt.Errorf("get substitution chains for package %q: %v", packageName, err)
			}
			for _, orig := range originals {
				from, to := orig, chains[orig]
				for to != "" {
					pkgOut.Bundles = append(pkgOut.Bundles, packageBundles[to].ToFBC())
					a.Log.Infof("adding substitution: %q supercedes %q", to, from)
					addSubsFor(&pkgOut, from, to)
					to = chains[to]
				}
			}
		}
		updateFBCPackage(fbc, pkgOut)
	}
	if _, err := declcfg.ConvertToModel(*fbc); err != nil {
		return fmt.Errorf("updated file-based catalog is invalid: %v", err)
	}
	a.Log.Infof("Writing updated file-based catalog")
	return writeToFS(*fbc, a.FromDir, declcfg.WriteYAML)
}

type bundle struct {
	registry.Bundle

	Version        semver.Version
	Replaces       string
	Skips          []string
	SkipRange      string
	SubstitutesFor string
	Icon           *declcfg.Icon
	Description    string
	Properties     []property.Property
	RelatedImages  []declcfg.RelatedImage
	ObjectStrings  []string
	CsvJSON        string
}

func (a Add) loadBundles(ctx context.Context, reg image.Registry, bundleImages []string) (map[string][]bundle, error) {
	bundlesMap := map[string][]bundle{}
	for _, bi := range bundleImages {
		a.Log.Infof("pulling bundle %q", bi)
		rBundle, err := getRegistryBundle(ctx, reg, bi)
		if err != nil {
			return nil, fmt.Errorf("get registry bundle for image %q: %v", bi, err)
		}
		b, err := newBundle(rBundle)
		if err != nil {
			return nil, err
		}
		bundlesMap[rBundle.Package] = append(bundlesMap[rBundle.Package], *b)
	}
	return bundlesMap, nil
}

func newBundle(rBundle *registry.Bundle) (*bundle, error) {
	version, err := rBundle.Version()
	if err != nil {
		return nil, fmt.Errorf("get version for bundle %q: %v", rBundle.Name, err)
	}
	semVersion, err := semver.Parse(version)
	if err != nil {
		return nil, fmt.Errorf("parse version %q for bundle %q as semver: %v", version, rBundle.Name, err)
	}
	replaces, err := rBundle.Replaces()
	if err != nil {
		return nil, fmt.Errorf("get replaces for bundle %q: %v", rBundle.Name, err)
	}
	skips, err := rBundle.Skips()
	if err != nil {
		return nil, fmt.Errorf("get skips for bundle %q: %v", rBundle.Name, err)
	}
	skipRange, err := rBundle.SkipRange()
	if err != nil {
		return nil, fmt.Errorf("get skipRange for bundle %q: %v", rBundle.Name, err)
	}
	subsFor, err := rBundle.SubstitutesFor()
	if err != nil {
		return nil, fmt.Errorf("get substitutesFor for bundle %q: %v", rBundle.Name, err)
	}
	icons, err := rBundle.Icons()
	if err != nil {
		return nil, fmt.Errorf("get icons for bundle %q: %v", rBundle.Name, err)
	}
	var icon *declcfg.Icon
	if len(icons) > 0 && len(icons[0].Base64data) > 0 {
		icon = &declcfg.Icon{
			Data:      icons[0].Base64data,
			MediaType: icons[0].MediaType,
		}
	}
	desc, err := rBundle.Description()
	if err != nil {
		return nil, fmt.Errorf("get description for bundle %q: %v", rBundle.Name, err)
	}

	properties, err := registry.PropertiesFromBundle(rBundle)
	if err != nil {
		return nil, fmt.Errorf("get properties for bundle %q: %v", rBundle.Name, err)
	}

	relatedImages, err := getRelatedImages(rBundle)
	if err != nil {
		return nil, fmt.Errorf("get related images for bundle %q: %v", rBundle.Name, err)
	}
	objStrings := []string{}
	csvJSON := ""
	for _, obj := range rBundle.Objects {
		data, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("marshal object for bundle %q: %v", rBundle.Name, err)
		}
		objStrings = append(objStrings, string(data))
		if obj.GroupVersionKind().Kind == "ClusterServiceVersion" {
			csvJSON = string(data)
		}
	}
	return &bundle{
		Bundle:         *rBundle,
		Version:        semVersion,
		Replaces:       replaces,
		Skips:          skips,
		SkipRange:      skipRange,
		SubstitutesFor: subsFor,
		Icon:           icon,
		Description:    desc,
		Properties:     properties,
		RelatedImages:  relatedImages,
		ObjectStrings:  objStrings,
		CsvJSON:        csvJSON,
	}, nil
}

func (b bundle) ToFBC() declcfg.Bundle {
	for _, obj := range b.ObjectStrings {
		b.Properties = append(b.Properties, property.MustBuildBundleObjectData([]byte(obj)))
	}
	return declcfg.Bundle{
		Schema:        "olm.bundle",
		Name:          b.Name,
		Package:       b.Package,
		Image:         b.BundleImage,
		Properties:    b.Properties,
		RelatedImages: b.RelatedImages,
		CsvJSON:       b.CsvJSON,
		Objects:       b.ObjectStrings,
	}
}

func (b bundle) ToModel(pkg *model.Package, ch *model.Channel) *model.Bundle {
	for _, obj := range b.ObjectStrings {
		b.Properties = append(b.Properties, property.MustBuildBundleObjectData([]byte(obj)))
	}
	ri := []model.RelatedImage{}
	for _, bri := range b.RelatedImages {
		ri = append(ri, model.RelatedImage{
			Name:  bri.Name,
			Image: bri.Image,
		})
	}
	return &model.Bundle{
		Package:       pkg,
		Channel:       ch,
		Name:          b.Name,
		Image:         b.BundleImage,
		Replaces:      b.Replaces,
		Skips:         b.Skips,
		SkipRange:     b.SkipRange,
		Properties:    b.Properties,
		RelatedImages: ri,
		Objects:       b.ObjectStrings,
		CsvJSON:       b.CsvJSON,
		PropertiesP:   nil,
		Version:       b.Version,
	}
}

func getRegistryBundle(ctx context.Context, reg image.Registry, img string) (*registry.Bundle, error) {
	ref := image.SimpleReference(img)
	if err := reg.Pull(ctx, ref); err != nil {
		return nil, err
	}
	tmpDir, err := os.MkdirTemp("", "dcm-render-bundle-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	if err := reg.Unpack(ctx, ref, tmpDir); err != nil {
		return nil, err
	}
	ii, err := registry.NewImageInput(ref, tmpDir)
	if err != nil {
		return nil, err
	}
	return ii.Bundle, nil
}

func getRelatedImages(b *registry.Bundle) ([]declcfg.RelatedImage, error) {
	csv, err := b.ClusterServiceVersion()
	if err != nil {
		return nil, err
	}

	var objmap map[string]*json.RawMessage
	if err = json.Unmarshal(csv.Spec, &objmap); err != nil {
		return nil, err
	}

	rawValue, ok := objmap["relatedImages"]
	if !ok || rawValue == nil {
		return nil, err
	}

	var relatedImages []declcfg.RelatedImage
	if err = json.Unmarshal(*rawValue, &relatedImages); err != nil {
		return nil, err
	}

	// Keep track of the images we've already found, so that we don't add
	// them multiple times.
	allImages := sets.NewString()
	for _, ri := range relatedImages {
		allImages = allImages.Insert(ri.Image)
	}

	if !allImages.Has(b.BundleImage) {
		relatedImages = append(relatedImages, declcfg.RelatedImage{
			Image: b.BundleImage,
		})
	}

	opImages, err := csv.GetOperatorImages()
	if err != nil {
		return nil, err
	}
	for img := range opImages {
		if !allImages.Has(img) {
			relatedImages = append(relatedImages, declcfg.RelatedImage{
				Image: img,
			})
		}
		allImages = allImages.Insert(img)
	}

	return relatedImages, nil
}
