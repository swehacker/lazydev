package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/swehacker/lazydev/internal/model"
)

type crictlImageList struct {
	Images []struct {
		ID       string   `json:"id"`
		RepoTags []string `json:"repoTags"`
	} `json:"images"`
}

type crictlInspect struct {
	Status struct {
		ID       string   `json:"id"`
		RepoTags []string `json:"repoTags"`
	} `json:"status"`
	Info struct {
		ImageSpec struct {
			Created time.Time `json:"created"`
			RootFS  struct {
				DiffIDs []string `json:"diff_ids"`
			} `json:"rootfs"`
		} `json:"imageSpec"`
	} `json:"info"`
}

// parseCrictlImages maps normalized repo tags to image IDs.
func parseCrictlImages(data []byte) (map[string]string, error) {
	var list crictlImageList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse crictl images: %w", err)
	}
	out := map[string]string{}
	for _, img := range list.Images {
		for _, tag := range img.RepoTags {
			out[model.NormalizeImageRef(tag)] = img.ID
		}
	}
	return out, nil
}

// parseCrictlInspect accepts the single-object or array output of `crictl inspecti -o json`.
func parseCrictlInspect(data []byte) ([]crictlInspect, error) {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '[' {
		var many []crictlInspect
		if err := json.Unmarshal(data, &many); err != nil {
			return nil, fmt.Errorf("parse crictl inspecti: %w", err)
		}
		return many, nil
	}
	var one crictlInspect
	if err := json.Unmarshal(data, &one); err != nil {
		return nil, fmt.Errorf("parse crictl inspecti: %w", err)
	}
	return []crictlInspect{one}, nil
}

func (s *Source) exec(ctx context.Context, containerName string, cmd ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	created, err := s.cli.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return nil, err
	}
	resp, err := s.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return nil, err
	}
	info, err := s.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	if info.ExitCode != 0 {
		return nil, fmt.Errorf("%s: exit %d: %s", strings.Join(cmd, " "), info.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// refreshFingerprints computes layer fingerprints of local tagged images and of the
// same images inside each k3d node, so drift can be detected despite differing digests.
func (s *Source) refreshFingerprints(ctx context.Context, images []model.Image, nodes []string) {
	s.fpMu.Lock()
	defer s.fpMu.Unlock()
	local := map[string]model.ImageFingerprint{}
	for _, img := range images {
		if img.Dangling {
			continue
		}
		fp, ok := s.localFP[img.ID]
		if !ok {
			inspect, err := s.cli.ImageInspect(ctx, img.ID)
			if err != nil {
				continue
			}
			fp = model.ImageFingerprint{ID: img.ID, Layers: inspect.RootFS.Layers, Created: img.Created}
			s.localFP[img.ID] = fp
		}
		for _, tag := range img.Tags {
			local[model.NormalizeImageRef(tag)] = fp
		}
	}

	nodeImages := map[string]model.NodeImages{}
	var errs []string
	for _, node := range nodes {
		imgs, err := s.nodeImages(ctx, node, local)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", node, err))
			continue
		}
		nodeImages[node] = imgs
	}

	drift := model.DriftState{Local: local, Nodes: nodeImages, Err: strings.Join(errs, "; ")}
	s.store.Apply(func(snap *model.Snapshot) { snap.Drift = drift })
}

func (s *Source) nodeImages(ctx context.Context, node string, local map[string]model.ImageFingerprint) (model.NodeImages, error) {
	out, err := s.exec(ctx, node, "crictl", "images", "-o", "json")
	if err != nil {
		return nil, err
	}
	refs, err := parseCrictlImages(out)
	if err != nil {
		return nil, err
	}
	result := model.NodeImages{}
	var unknown []string
	for ref, id := range refs {
		if _, interesting := local[ref]; !interesting {
			continue
		}
		if fp, ok := s.nodeFP[id]; ok {
			result[ref] = fp
		} else {
			unknown = append(unknown, ref)
		}
	}
	if len(unknown) > 0 {
		out, err := s.exec(ctx, node, append([]string{"crictl", "inspecti", "-o", "json"}, unknown...)...)
		if err != nil {
			return nil, err
		}
		inspected, err := parseCrictlInspect(out)
		if err != nil {
			return nil, err
		}
		for _, in := range inspected {
			fp := model.ImageFingerprint{ID: in.Status.ID, Layers: in.Info.ImageSpec.RootFS.DiffIDs, Created: in.Info.ImageSpec.Created}
			s.nodeFP[fp.ID] = fp
		}
		for _, ref := range unknown {
			if fp, ok := s.nodeFP[refs[ref]]; ok {
				result[ref] = fp
			}
		}
	}
	return result, nil
}
