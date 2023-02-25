package gateway

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"syscall"
	"time"

	"github.com/TykTechnologies/tyk/config"
)

type ConfigPayload struct {
	Configuration config.Config
	ForHostname   string
	ForNodeID     string
	TimeStamp     int64
}

func (gw *Gateway) backupConfiguration() error {
	oldConfig, err := json.MarshalIndent(gw.GetConfig(), "", "    ")
	if err != nil {
		return err
	}

	now := time.Now()
	asStr := now.Format("Mon-Jan-_2-15-04-05-2006")
	fName := asStr + ".tyk.conf"
	return ioutil.WriteFile(fName, oldConfig, 0644)
}

func writeNewConfiguration(payload ConfigPayload) error {
	newConfig, err := json.MarshalIndent(payload.Configuration, "", "    ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(confPaths[0], newConfig, 0644)
}

func (gw *Gateway) handleNewConfiguration(payload string) {
	// Decode the configuration from the payload
	configPayload := ConfigPayload{}

	// We actually want to merge into the existing configuration
	// so as not to lose data through automatic defaults
	config.Load(confPaths, &configPayload.Configuration)

	err := json.Unmarshal([]byte(payload), &configPayload)
	if err != nil {
		pubSubLog.WithError(err).Error("Failed to decode configuration payload")
		return
	}

	// Make sure payload matches nodeID and hostname
	if configPayload.ForHostname != gw.hostDetails.Hostname && configPayload.ForNodeID != gw.GetNodeID() {
		pubSubLog.Info("Configuration update received, no NodeID/Hostname match found")
		return
	}

	if !gw.GetConfig().AllowRemoteConfig {
		pubSubLog.Warning("Ignoring new config: Remote configuration is not allowed for this node.")
		return
	}

	if err := gw.backupConfiguration(); err != nil {
		pubSubLog.WithError(err).Error("Failed to backup existing configuration")
		return
	}

	if err := writeNewConfiguration(configPayload); err != nil {
		pubSubLog.WithError(err).Error("Failed to write new configuration")
		return
	}

	pubSubLog.Info("Initiating configuration reload")

	myPID := gw.hostDetails.PID
	if myPID == 0 {
		pubSubLog.Error("No PID found, cannot reload")
		return
	}

	pubSubLog.Infof("Sending reload signal to PID: %d", myPID)
	if err := syscall.Kill(myPID, syscall.SIGUSR2); err != nil {
		pubSubLog.Error("Process reload failed: ", err)
	}
}

type GetConfigPayload struct {
	FromHostname string
	FromNodeID   string
	TimeStamp    int64
}

type ReturnConfigPayload struct {
	FromHostname  string
	FromNodeID    string
	Configuration map[string]interface{}
	TimeStamp     int64
}

func sanitizeConfig(mc map[string]interface{}) map[string]interface{} {
	sanitzeFields := []string{
		"secret",
		"node_secret",
		"storage",
		"slave_options",
		"auth_override",
	}
	for _, field_name := range sanitzeFields {
		delete(mc, field_name)
	}
	return mc
}

func (gw *Gateway) getExistingConfig() (map[string]interface{}, error) {
	f, err := os.Open(gw.GetConfig().OriginalPath)
	if err != nil {
		return nil, err
	}
	var microConfig map[string]interface{}
	if err := json.NewDecoder(f).Decode(&microConfig); err != nil {
		return nil, err
	}
	return sanitizeConfig(microConfig), nil
}

func (gw *Gateway) handleSendMiniConfig(payload string) {
	// Decode the configuration from the payload
	configPayload := GetConfigPayload{}
	err := json.Unmarshal([]byte(payload), &configPayload)
	if err != nil {
		pubSubLog.Error("Failed unmarshal request: ", err)
		return
	}

	// Make sure payload matches nodeID and hostname
	if configPayload.FromHostname != gw.hostDetails.Hostname && configPayload.FromNodeID != gw.GetNodeID() {
		pubSubLog.Debug("Configuration request received, no NodeID/Hostname match found, ignoring")
		return
	}

	config, err := gw.getExistingConfig()
	if err != nil {
		pubSubLog.Error("Failed to get existing configuration: ", err)
		return
	}

	returnPayload := ReturnConfigPayload{
		FromHostname:  gw.hostDetails.Hostname,
		FromNodeID:    gw.GetNodeID(),
		Configuration: config,
		TimeStamp:     time.Now().Unix(),
	}

	payloadAsJSON, err := json.Marshal(returnPayload)
	if err != nil {
		pubSubLog.Error("Failed to get marshal configuration: ", err)
		return
	}

	asNotification := Notification{
		Command: NoticeGatewayConfigResponse,
		Payload: string(payloadAsJSON),
		Gw:      gw,
	}

	gw.MainNotifier.Notify(asNotification)
	pubSubLog.Debug("Configuration request responded.")

}
