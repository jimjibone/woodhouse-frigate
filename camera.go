package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jimjibone/log"
	"github.com/jimjibone/wh/v2/bridges"
	"github.com/jimjibone/wh/v2/bridges/services"
	"github.com/jimjibone/wh/v2/types"
	clientsapi "github.com/jimjibone/woodhouse-api/go/v2/clients"
	"github.com/jimjibone/woodhouse-frigate/api"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// ImageFetcher gets the latest snapshot of a camera from Frigate.
type ImageFetcher func(ctx context.Context, serverAddr, cameraName string) ([]byte, error)

// FrigateCamera is one Frigate camera, published as a device with endpoint 1
// holding camera (snapshot + detection events) and motion services.
type FrigateCamera struct {
	log        *log.Context
	serverAddr string
	name       string
	fetch      ImageFetcher
	dev        *bridges.Device
	motion     *services.Motion
	camera     *services.Camera
}

// NewFrigateCamera creates the camera device and adds it to the bridge.
func NewFrigateCamera(serverAddr, name string, client *bridges.Bridge) (*FrigateCamera, error) {
	cam, err := newFrigateCamera(serverAddr, name, api.GetLatestImage)
	if err != nil {
		return nil, err
	}
	if err := client.AddDevice(cam.dev); err != nil {
		cam.dev.Close()
		return nil, err
	}
	return cam, nil
}

// newFrigateCamera builds the camera device without registering it.
func newFrigateCamera(serverAddr, name string, fetch ImageFetcher) (*FrigateCamera, error) {
	id, err := bridges.DeviceID("frigate", name)
	if err != nil {
		return nil, fmt.Errorf("camera %q: %w", name, err)
	}
	cam := &FrigateCamera{
		log:        log.NewContext(log.DefaultLogger, name, log.DebugLevel),
		serverAddr: serverAddr,
		name:       name,
		fetch:      fetch,
		dev:        bridges.NewDevice(id, clientsapi.Device_DEVICE),
		motion:     services.NewMotion(""),
		camera:     services.NewCamera("", services.CameraFeatureSnapshot),
	}
	caser := cases.Title(language.Und, cases.NoLower)
	cam.dev.Info.Name.Set(caser.String(name))
	cam.dev.Endpoint("1").AddService(cam.camera, cam.motion)
	cam.motion.SetAlias("Motion")
	cam.motion.Motion.Set(false)
	cam.camera.Image.OnImageRequest(cam.handleCameraImageRequest)
	return cam, nil
}

func (cam *FrigateCamera) HandleMotion(payload json.RawMessage) {
	switch string(payload) {
	case `"OFF"`:
		cam.log.Infof("motion not detected")
		cam.motion.Motion.Set(false)
	case `"ON"`:
		cam.log.Infof("motion was detected")
		// Only the transition to ON is a new detection.
		if cam.motion.Motion.Set(true) {
			if err := cam.motion.EmitDetected(); err != nil {
				cam.log.Errorf("failed to emit motion detected: %s", err)
			}
		}
	default:
		cam.log.Errorf("failed to parse motion state of: %s", payload)
	}
}

// HandleEvent turns a Frigate tracked-object event into a camera detection
// event. Only the first sighting ("new") of a real object is reported; updates
// and ends of the same object, and false positives, are ignored.
func (cam *FrigateCamera) HandleEvent(ev api.Event) {
	if ev.Type != "new" || ev.After.FalsePositive || ev.After.Label == "" {
		return
	}
	det := types.CameraDetectionEvent{Label: ev.After.Label}
	score := ev.After.Score
	if score <= 0 {
		score = ev.After.TopScore
	}
	if score > 0 {
		score = min(score, 1)
		det.Score = &score
	}
	if len(ev.After.CurrentZones) > 0 {
		zone := ev.After.CurrentZones[0]
		det.Zone = &zone
	}
	cam.log.Infof("detected %s", det.Label)
	if err := cam.camera.EmitDetection(det); err != nil {
		cam.log.Errorf("failed to emit detection: %s", err)
	}
}

func (cam *FrigateCamera) handleCameraImageRequest() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := cam.fetch(ctx, cam.serverAddr, cam.name)
	if err != nil {
		cam.log.Errorf("failed to get latest image: %s", err)
	} else {
		cam.log.Infof("got latest image: %d bytes", len(data))
	}
	return data, err
}
