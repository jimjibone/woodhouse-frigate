package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jimjibone/wh/v2/bridges"
	"github.com/jimjibone/wh/v2/types"
	clientsapi "github.com/jimjibone/woodhouse-api/go/v2/clients"
	"github.com/jimjibone/woodhouse-frigate/api"
)

func fakeFetch(data []byte, err error, gotName *string) ImageFetcher {
	return func(ctx context.Context, serverAddr, cameraName string) ([]byte, error) {
		if gotName != nil {
			*gotName = serverAddr + "/" + cameraName
		}
		return data, err
	}
}

func newTestCamera(t *testing.T, name string, fetch ImageFetcher) (*FrigateCamera, <-chan *clientsapi.Device) {
	t.Helper()
	cam, err := newFrigateCamera("192.0.2.10:5000", name, fetch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cam.dev.Close)
	ch := make(chan *clientsapi.Device, 16)
	cam.dev.Init(func(state *clientsapi.Device) { ch <- state })

	// Drop the construction-time attribute updates, so tests see only what
	// they cause.
	time.Sleep(100 * time.Millisecond)
	for len(ch) > 0 {
		<-ch
	}
	return cam, ch
}

func next(t *testing.T, ch <-chan *clientsapi.Device) *clientsapi.Device {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(time.Second):
		t.Fatal("nothing sent")
		return nil
	}
}

func none(t *testing.T, ch <-chan *clientsapi.Device) {
	t.Helper()
	select {
	case d := <-ch:
		t.Fatalf("unexpected update: %v", d)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDeviceID(t *testing.T) {
	for name, want := range map[string]string{
		"front":      "frigatefront",
		"garden-cam": "frigategardencam",
		"Back Yard":  "frigatebackyard",
	} {
		cam, _ := newTestCamera(t, name, fakeFetch(nil, nil, nil))
		if got := cam.dev.ID(); got != want {
			t.Errorf("%q: id %q, want %q", name, got, want)
		}
	}
	if _, err := newFrigateCamera("192.0.2.10:5000", "---", nil); err == nil {
		t.Error("camera name without [a-z0-9] accepted")
	}
}

func TestDeviceShape(t *testing.T) {
	cam, ch := newTestCamera(t, "garden-cam", fakeFetch(nil, nil, nil))
	cam.dev.SendFullState()
	full := next(t, ch)
	if !full.FullState || len(full.Endpoints) != 2 {
		t.Fatalf("full state: %v", full)
	}
	root, ep := full.Endpoints[0], full.Endpoints[1]
	if root.Id != bridges.RootEndpointID || len(root.Services) != 2 ||
		root.Services[0].Typ != clientsapi.Service_INFO || root.Services[1].Typ != clientsapi.Service_ONLINE {
		t.Fatalf("root endpoint: %v", root)
	}
	if ep.Id != "1" || len(ep.Services) != 2 ||
		ep.Services[0].Typ != clientsapi.Service_CAMERA || ep.Services[1].Typ != clientsapi.Service_MOTION {
		t.Fatalf("endpoint 1: %v", ep)
	}
	if f := ep.Services[0].Features; len(f) != 1 || f[0] != "snapshot" {
		t.Fatalf("camera features: %v", f)
	}
	if cam.motion.Motion.Get() {
		t.Error("motion starts true")
	}
}

func TestMotionEvents(t *testing.T) {
	cam, ch := newTestCamera(t, "front", fakeFetch(nil, nil, nil))

	cam.HandleMotion(json.RawMessage(`"ON"`))
	up := next(t, ch)
	if len(up.Events) != 1 {
		t.Fatalf("want one event, got %v", up)
	}
	e := up.Events[0]
	if e.EndpointId != "1" || e.ServiceId != "motion" || e.Id != "detected" {
		t.Fatalf("event: %v", e)
	}
	if !cam.motion.Motion.Get() {
		t.Error("motion not set")
	}

	// A repeated ON is not a new detection.
	cam.HandleMotion(json.RawMessage(`"ON"`))
	none(t, ch)

	cam.HandleMotion(json.RawMessage(`"OFF"`))
	up = next(t, ch)
	if len(up.Events) != 0 || cam.motion.Motion.Get() {
		t.Fatalf("OFF: %v", up)
	}

	// Garbage changes nothing.
	cam.HandleMotion(json.RawMessage(`"MAYBE"`))
	none(t, ch)
}

func detectionPayload(typ, camera, label string, score float64, zones ...string) json.RawMessage {
	obj := map[string]any{
		"id": "1700000000.1-abc123", "camera": camera, "label": label,
		"score": score, "top_score": score, "false_positive": false, "current_zones": zones,
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "before": obj, "after": obj})
	return b
}

func TestDetectionEvent(t *testing.T) {
	cam, ch := newTestCamera(t, "front", fakeFetch(nil, nil, nil))

	// Object as a JSON object and as a JSON-encoded string.
	raw := detectionPayload("new", "front", "person", 0.87, "porch", "lawn")
	quoted, _ := json.Marshal(string(raw))
	for _, payload := range []json.RawMessage{raw, quoted} {
		ev, err := api.ParseEvent(payload)
		if err != nil {
			t.Fatal(err)
		}
		cam.HandleEvent(ev)
		up := next(t, ch)
		if len(up.Events) != 1 {
			t.Fatalf("want one event, got %v", up)
		}
		e := up.Events[0]
		if e.EndpointId != "1" || e.ServiceId != "camera" || e.Id != "detection" {
			t.Fatalf("event: %v", e)
		}
		got := types.ParseCameraDetectionEvent(e.Fields)
		if got.Label != "person" || got.Score == nil || *got.Score != 0.87 || got.Zone == nil || *got.Zone != "porch" {
			t.Fatalf("fields: %+v", got)
		}
	}
}

func TestDetectionEventOptionalFields(t *testing.T) {
	cam, ch := newTestCamera(t, "front", fakeFetch(nil, nil, nil))
	ev, err := api.ParseEvent(detectionPayload("new", "front", "car", 0))
	if err != nil {
		t.Fatal(err)
	}
	cam.HandleEvent(ev)
	got := types.ParseCameraDetectionEvent(next(t, ch).Events[0].Fields)
	if got.Label != "car" || got.Score != nil || got.Zone != nil {
		t.Fatalf("fields: %+v", got)
	}
}

func TestDetectionIgnored(t *testing.T) {
	cam, ch := newTestCamera(t, "front", fakeFetch(nil, nil, nil))
	for _, typ := range []string{"update", "end"} {
		ev, err := api.ParseEvent(detectionPayload(typ, "front", "person", 0.9))
		if err != nil {
			t.Fatal(err)
		}
		cam.HandleEvent(ev)
	}
	ev, _ := api.ParseEvent(detectionPayload("new", "front", "person", 0.9))
	ev.After.FalsePositive = true
	cam.HandleEvent(ev)
	none(t, ch)
}

func TestParseEventBad(t *testing.T) {
	if _, err := api.ParseEvent(json.RawMessage(`"not json"`)); err == nil {
		t.Error("bad event accepted")
	}
}

func TestImageRequest(t *testing.T) {
	jpeg := []byte("fake-jpeg-bytes")
	var got string
	cam, _ := newTestCamera(t, "garden-cam", fakeFetch(jpeg, nil, &got))
	req := &clientsapi.ImageRequest{EndpointId: "1", ServiceId: "camera", AttributeId: "image"}
	data, err := cam.dev.HandleImage(req, func(*clientsapi.ImageResponse) {})
	if err != nil || string(data) != string(jpeg) {
		t.Fatalf("image: %q, %v", data, err)
	}
	// Frigate is asked by the raw camera name, not the sanitised id.
	if got != "192.0.2.10:5000/garden-cam" {
		t.Errorf("fetched %q", got)
	}
}

func TestImageRequestError(t *testing.T) {
	cam, _ := newTestCamera(t, "front", fakeFetch(nil, errors.New("boom"), nil))
	req := &clientsapi.ImageRequest{EndpointId: "1", ServiceId: "camera", AttributeId: "image"}
	if _, err := cam.dev.HandleImage(req, func(*clientsapi.ImageResponse) {}); err == nil {
		t.Fatal("fetch error not returned")
	}
}
