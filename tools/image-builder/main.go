package main

import (
	"archive/tar"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const version = "0.5.0"

var created = time.Unix(0, 0).UTC()

type layerFile struct {
	name string
	path string
	mode int64
	uid  int
	gid  int
}

type imageSpec struct {
	name         string
	base         v1.Image
	files        []layerFile
	entrypoint   []string
	exposedPorts []string
	environment  []string
}

func main() {
	root := flag.String("root", ".", "repository root")
	output := flag.String("output", "outputs/images", "output directory")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		log.Fatal(err)
	}
	absOutput, err := filepath.Abs(*output)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(absOutput, 0o755); err != nil {
		log.Fatal(err)
	}

	api := filepath.Join(absRoot, "work", "dist", "art-api-linux-amd64")
	hbbs := filepath.Join(absRoot, "work", "target-linux", "x86_64-unknown-linux-musl", "release", "art-hbbs")
	hbbr := filepath.Join(absRoot, "work", "target-linux", "x86_64-unknown-linux-musl", "release", "art-hbbr")
	entrypoint := filepath.Join(absRoot, "docker", "all-in-one-entrypoint.sh")
	for _, path := range []string{api, hbbs, hbbr, entrypoint} {
		if _, err := os.Stat(path); err != nil {
			log.Fatalf("required build artifact %s: %v", path, err)
		}
	}

	specs := []imageSpec{
		{name: "rustdesk-server-api", base: empty.Image, files: []layerFile{{"art-api", api, 0o555, 0, 0}}, entrypoint: []string{"/art-api"}, exposedPorts: []string{"21114/tcp"}},
		{name: "rustdesk-server-hbbs", base: empty.Image, files: []layerFile{{"art-hbbs", hbbs, 0o555, 0, 0}}, entrypoint: []string{"/art-hbbs"}, exposedPorts: []string{"21115/tcp", "21116/tcp", "21116/udp"}},
		{name: "rustdesk-server-hbbr", base: empty.Image, files: []layerFile{{"art-hbbr", hbbr, 0o555, 0, 0}}, entrypoint: []string{"/art-hbbr"}, exposedPorts: []string{"21117/tcp", "21119/tcp", "21119/udp"}},
	}

	alpine, err := remote.Image(
		name.MustParseReference("docker.io/library/alpine:3.23.5"),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}),
		remote.WithContext(context.Background()),
	)
	if err != nil {
		log.Fatalf("load Alpine base: %v", err)
	}
	specs = append(specs, imageSpec{
		name: "rustdesk-server-routeros", base: alpine,
		files: []layerFile{
			{"usr/local/bin/art-api", api, 0o555, 0, 0},
			{"usr/local/bin/art-hbbs", hbbs, 0o555, 0, 0},
			{"usr/local/bin/art-hbbr", hbbr, 0o555, 0, 0},
			{"usr/local/bin/art-entrypoint", entrypoint, 0o555, 0, 0},
		},
		entrypoint:   []string{"/usr/local/bin/art-entrypoint"},
		exposedPorts: []string{"21114/tcp", "21115/tcp", "21116/tcp", "21116/udp", "21117/tcp", "21118/tcp", "21119/tcp", "21119/udp"},
		environment:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
	})

	for _, spec := range specs {
		path := filepath.Join(absOutput, fmt.Sprintf("%s-%s-linux-amd64.tar", spec.name, version))
		if err := build(path, spec); err != nil {
			log.Fatalf("build %s: %v", spec.name, err)
		}
		fmt.Println(path)
	}
}

func build(output string, spec imageSpec) error {
	layer, err := makeLayer(spec.files)
	if err != nil {
		return err
	}
	image, err := mutate.AppendLayers(spec.base, layer)
	if err != nil {
		return err
	}
	config, err := image.ConfigFile()
	if err != nil {
		return err
	}
	config.Architecture = "amd64"
	config.OS = "linux"
	config.Created = v1.Time{Time: created}
	config.Config.User = "65532:65532"
	config.Config.Entrypoint = spec.entrypoint
	config.Config.Cmd = nil
	config.Config.WorkingDir = "/"
	config.Config.Volumes = map[string]struct{}{"/data": {}}
	config.Config.ExposedPorts = make(map[string]struct{}, len(spec.exposedPorts))
	for _, port := range spec.exposedPorts {
		config.Config.ExposedPorts[port] = struct{}{}
	}
	if len(spec.environment) > 0 {
		config.Config.Env = spec.environment
	}
	if config.Config.Labels == nil {
		config.Config.Labels = make(map[string]string)
	}
	config.Config.Labels["org.opencontainers.image.title"] = spec.name
	config.Config.Labels["org.opencontainers.image.version"] = version
	config.Config.Labels["org.opencontainers.image.description"] = "RustDesk Server RouterOS"
	image, err = mutate.ConfigFile(image, config)
	if err != nil {
		return err
	}
	ref, err := name.ParseReference("local/" + spec.name + ":" + version)
	if err != nil {
		return err
	}
	return tarball.WriteToFile(output, ref, image)
}

func makeLayer(files []layerFile) (v1.Layer, error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	directories := []struct {
		name     string
		uid, gid int
	}{{"data", 65532, 65532}, {"usr", 0, 0}, {"usr/local", 0, 0}, {"usr/local/bin", 0, 0}}
	for _, directory := range directories {
		if err := writer.WriteHeader(&tar.Header{Name: directory.name, Typeflag: tar.TypeDir, Mode: 0o755, Uid: directory.uid, Gid: directory.gid, ModTime: created, Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
	}
	for _, file := range files {
		content, err := os.Open(file.path)
		if err != nil {
			return nil, err
		}
		info, err := content.Stat()
		if err != nil {
			content.Close()
			return nil, err
		}
		header := &tar.Header{Name: file.name, Typeflag: tar.TypeReg, Mode: file.mode, Uid: file.uid, Gid: file.gid, Size: info.Size(), ModTime: created, Format: tar.FormatPAX}
		if err := writer.WriteHeader(header); err != nil {
			content.Close()
			return nil, err
		}
		if _, err := io.Copy(writer, content); err != nil {
			content.Close()
			return nil, err
		}
		if err := content.Close(); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return tarball.LayerFromReader(bytes.NewReader(buffer.Bytes()))
}
