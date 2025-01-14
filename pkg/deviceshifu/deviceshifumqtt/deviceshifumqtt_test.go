package deviceshifumqtt

import (
	"context"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"

	"errors"
	"github.com/edgenesis/shifu/pkg/k8s/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"io"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/edgenesis/shifu/pkg/deviceshifu/deviceshifubase"
	"github.com/edgenesis/shifu/pkg/deviceshifu/unitest"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	v1 "k8s.io/api/apps/v1"
)

func TestMain(m *testing.M) {
	err := GenerateConfigMapFromSnippet(MockDeviceCmStr, MockDeviceConfigFolder)
	if err != nil {
		klog.Errorf("error when generateConfigmapFromSnippet,err: %v", err)
		os.Exit(-1)
	}
	m.Run()
	err = os.RemoveAll(MockDeviceConfigPath)
	if err != nil {
		klog.Fatal(err)
	}
}

func TestStart(t *testing.T) {
	deviceShifuMetadata := &deviceshifubase.DeviceShifuMetaData{
		Name:           "TestStart",
		ConfigFilePath: "etc/edgedevice/config",
		KubeConfigPath: deviceshifubase.DeviceKubeconfigDoNotLoadStr,
		Namespace:      "",
	}

	mockds, err := New(deviceShifuMetadata)
	if err != nil {
		t.Errorf("Failed creating new deviceshifu %v", err.Error())
	}

	if err := mockds.Start(wait.NeverStop); err != nil {
		t.Errorf("DeviceShifu.Start failed due to: %v", err.Error())
	}

	if err := mockds.Stop(); err != nil {
		t.Errorf("unable to stop mock deviceShifu, error: %+v", err)
	}
}

func TestDeviceHealthHandler(t *testing.T) {
	deviceShifuMetadata := &deviceshifubase.DeviceShifuMetaData{
		Name:           "TeststartHTTPServer",
		ConfigFilePath: "etc/edgedevice/config",
		KubeConfigPath: deviceshifubase.DeviceKubeconfigDoNotLoadStr,
		Namespace:      "",
	}

	mockds, err := New(deviceShifuMetadata)
	if err != nil {
		t.Errorf("Failed creating new deviceshifu")
	}

	if err := mockds.Start(wait.NeverStop); err != nil {
		t.Errorf("DeviceShifu.Start failed due to: %v", err.Error())
	}

	resp, err := unitest.RetryAndGetHTTP("http://localhost:8080/health", 3)
	if err != nil {
		t.Errorf("HTTP GET returns an error %v", err.Error())
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Errorf("unable to read response body, error: %v", err.Error())
	}

	if string(body) != deviceshifubase.DeviceIsHealthyStr {
		t.Errorf("%+v", body)
	}

	if err := mockds.Stop(); err != nil {
		t.Errorf("unable to stop mock deviceShifu, error: %+v", err)
	}
}

func TestCommandHandleMQTTFunc(t *testing.T) {
	hs := mockHandlerServer(t)
	defer hs.Close()
	addr := strings.Split(hs.URL, "//")[1]
	mockHandlerHTTP := &DeviceCommandHandlerMQTT{
		HandlerMetaData: &HandlerMetaData{
			edgeDeviceSpec: v1alpha1.EdgeDeviceSpec{
				Address: &addr,
			},
		},
	}

	ds := mockDeviceServer(mockHandlerHTTP, t)
	defer ds.Close()
	dc := mockRestClient(ds.URL, "testing")

	// test post method
	r := dc.Post().Do(context.TODO())
	assert.Equal(t, "the server rejected our request for an unknown reason", r.Error().Error())

	// test Cannot Encode message to json
	mqttMessageStr = ""
	mqttMessageReceiveTimestamp = time.Now()
	r = dc.Get().Do(context.TODO())
	assert.Nil(t, r.Error())
}

func mockRestClient(host string, path string) *rest.RESTClient {
	c, err := rest.RESTClientFor(
		&rest.Config{
			Host:    host,
			APIPath: path,
			ContentConfig: rest.ContentConfig{
				GroupVersion:         &v1.SchemeGroupVersion,
				NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
			},
		},
	)
	if err != nil {
		klog.Errorf("mock client for host %s, apipath: %s failed,", host, path)
		return nil
	}

	return c
}

type MockCommandHandler interface {
	commandHandleFunc() http.HandlerFunc
}

func mockDeviceServer(h MockCommandHandler, t *testing.T) *httptest.Server {
	// catch device http request and response properly with specific paths
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/testing/apps/v1":
			klog.Info("ds get testing call, calling the handler server")
			assert.Equal(t, "/testing/apps/v1", path)
			f := h.commandHandleFunc()
			f.ServeHTTP(w, r)
		default:
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			klog.Info("ds default request, path:", path)
		}
	}))
	return server
}

func mockHandlerServer(t *testing.T) *httptest.Server {
	// catch handler http request and response properly with specific paths
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/test_instruction":
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			klog.Info("handler get the instruction and executed.")
		default:
			w.WriteHeader(http.StatusOK)
			klog.Info("hs get default request, path:", path)
		}

	}))
	return server
}

func TestCollectMQTTTelemetry(t *testing.T) {
	testCases := []struct {
		Name        string
		inputDevice *DeviceShifu
		expected    bool
		err         error
	}{
		{
			"case 1 Protocol is nil",
			&DeviceShifu{
				base: &deviceshifubase.DeviceShifuBase{
					Name: "test",
					EdgeDevice: &v1alpha1.EdgeDevice{
						Spec: v1alpha1.EdgeDeviceSpec{
							Address: unitest.ToPointer("localhost"),
						},
					},
				},
			},
			false,
			nil,
		},
		{
			"case 2 Address is nil",
			&DeviceShifu{
				base: &deviceshifubase.DeviceShifuBase{
					Name: "test",
					EdgeDevice: &v1alpha1.EdgeDevice{
						Spec: v1alpha1.EdgeDeviceSpec{
							Protocol: unitest.ToPointer(v1alpha1.ProtocolMQTT),
						},
					},
					DeviceShifuConfig: &deviceshifubase.DeviceShifuConfig{
						Telemetries: &deviceshifubase.DeviceShifuTelemetries{},
					},
				},
			},
			false,
			errors.New("Device test does not have an address"),
		},
		{
			"case 3 DeviceShifuTelemetry Update",
			&DeviceShifu{
				base: &deviceshifubase.DeviceShifuBase{
					Name: "test",
					EdgeDevice: &v1alpha1.EdgeDevice{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: "test_namespace",
						},
						Spec: v1alpha1.EdgeDeviceSpec{
							Address:  unitest.ToPointer("localhost"),
							Protocol: unitest.ToPointer(v1alpha1.ProtocolMQTT),
						},
					},
					DeviceShifuConfig: &deviceshifubase.DeviceShifuConfig{
						Telemetries: &deviceshifubase.DeviceShifuTelemetries{
							DeviceShifuTelemetrySettings: &deviceshifubase.DeviceShifuTelemetrySettings{
								DeviceShifuTelemetryUpdateIntervalInMilliseconds: unitest.ToPointer(time.Now().UnixMilli()),
							},
						},
					},
				},
			},
			true,
			nil,
		},
		{
			"case 4 Protocol is http",
			&DeviceShifu{
				base: &deviceshifubase.DeviceShifuBase{
					Name: "test",
					EdgeDevice: &v1alpha1.EdgeDevice{
						Spec: v1alpha1.EdgeDeviceSpec{
							Address:  unitest.ToPointer("localhost"),
							Protocol: unitest.ToPointer(v1alpha1.ProtocolHTTP),
						},
					},
				},
			},
			false,
			nil,
		},
		{
			"case 5 interval is nil",
			&DeviceShifu{
				base: &deviceshifubase.DeviceShifuBase{
					Name: "test",
					EdgeDevice: &v1alpha1.EdgeDevice{
						Spec: v1alpha1.EdgeDeviceSpec{
							Address:  unitest.ToPointer("localhost"),
							Protocol: unitest.ToPointer(v1alpha1.ProtocolMQTT),
						},
					},
					DeviceShifuConfig: &deviceshifubase.DeviceShifuConfig{
						Telemetries: &deviceshifubase.DeviceShifuTelemetries{
							DeviceShifuTelemetrySettings: &deviceshifubase.DeviceShifuTelemetrySettings{},
						},
					},
				},
			},
			true,
			nil,
		},
	}

	mqttMessageReceiveTimestamp = time.Now()
	for _, c := range testCases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := c.inputDevice.collectMQTTTelemetry()
			if got {
				assert.Equal(t, c.expected, got)
				assert.Nil(t, err)
			} else {
				assert.Equal(t, c.expected, got)
				assert.Equal(t, c.err, err)
			}
		})
	}
}
