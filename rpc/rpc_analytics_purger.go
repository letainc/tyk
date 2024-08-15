package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TykTechnologies/tyk-pump/analytics"

	"github.com/vmihailenco/msgpack"

	"github.com/TykTechnologies/tyk/storage"
)

const ANALYTICS_KEYNAME = "tyk-system-analytics"

// RPCPurger will purge analytics data into a Mongo database, requires that the Mongo DB string is specified
// in the Config object
type Purger struct {
	Store storage.Handler
}

// Connect Connects to RPC
func (r *Purger) Connect() {
	if !values.ClientIsConnected() {
		Log.Error("RPC client is not connected, use Connect method 1st")
	}

	// setup RPC func if needed
	if !addedFuncs["Ping"] {
		dispatcher.AddFunc("Ping", func() bool {
			return false
		})
		addedFuncs["Ping"] = true
	}
	if !addedFuncs["PurgeAnalyticsData"] {
		dispatcher.AddFunc("PurgeAnalyticsData", func(data string) error {
			return nil
		})
		addedFuncs["PurgeAnalyticsData"] = true
	}

	Log.Info("RPC Analytics client using singleton")
}

// PurgeLoop starts the loop that will pull data out of the in-memory
// store and into RPC.
func (r Purger) PurgeLoop(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.PurgeCache()
		}
	}
}

// PurgeCache will pull the data from the in-memory store and drop it into the specified MongoDB collection
func (r *Purger) PurgeCache() {

	if !values.ClientIsConnected() {
		Log.Error("RPC client is not connected, use Connect method 1st")
	}

	if _, err := RPC().FuncClientSingleton("Ping", nil); err != nil {
		Log.WithError(err).Error("Can't purge cache, failed to ping RPC")
		return
	}

	for i := -1; i < 10; i++ {
		var analyticsKeyName string
		if i == -1 {
			//if it's the first iteration, we look for tyk-system-analytics to maintain backwards compatibility or if analytics_config.enable_multiple_analytics_keys is disabled in the gateway
			analyticsKeyName = ANALYTICS_KEYNAME
		} else {
			// keyname + serializationmethod
			analyticsKeyName = fmt.Sprintf("%v_%v", ANALYTICS_KEYNAME, i)
		}

		analyticsValues := r.Store.GetAndDeleteSet(analyticsKeyName)
		if len(analyticsValues) == 0 {
			continue
		}
		keys, failedRecords := processAnalyticsValues(analyticsValues)
		Log.Debugf("could not decode %v records", failedRecords)

		data, err := json.Marshal(keys)
		if err != nil {
			Log.WithError(err).Error("Failed to marshal analytics data")
			return
		}

		// Send keys to RPC
		if _, err := RPC().FuncClientSingleton("PurgeAnalyticsData", string(data)); err != nil {
			RPC().EmitErrorEvent(FuncClientSingletonCall, "PurgeAnalyticsData", err)
			Log.Warn("Failed to call purge, retrying: ", err)
		}

	}
}

func processAnalyticsValues(analyticsValues []interface{}) ([]interface{}, int) {
	keys := make([]interface{}, len(analyticsValues))
	failedRecords := 0

	for i, v := range analyticsValues {
		decoded, err := decodeAnalyticsRecord(v)
		if err != nil {
			failedRecords++
			Log.WithError(err).Error("Couldn't unmarshal analytics data")
		} else {
			Log.WithField("decoded", decoded).Debug("Decoded Record")
			keys[i] = decoded
		}
	}

	return keys, failedRecords
}

func decodeAnalyticsRecord(encoded interface{}) (analytics.AnalyticsRecord, error) {
	decoded := analytics.AnalyticsRecord{}
	err := msgpack.Unmarshal([]byte(encoded.(string)), &decoded)
	if err != nil {
		return decoded, err
	}
	return decoded, nil
}
