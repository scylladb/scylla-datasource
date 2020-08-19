package main
import (
	"context"
	"encoding/json"
	"time"
	"gopkg.in/inf.v0"
	"strconv"
	"math/big"

	"fmt"
	"github.com/gocql/gocql"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/datasource"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/grafana/grafana-plugin-sdk-go/data"
)

// newDatasource returns datasource.ServeOpts.
func newDatasource() datasource.ServeOpts {
    log.DefaultLogger.Debug("Creating new datasource")
	// creates a instance manager for your plugin. The function passed
	// into `NewInstanceManger` is called when the instance is created
	// for the first time or when a datasource configuration changed.
	im := datasource.NewInstanceManager(newDataSourceInstance)
	ds := &SampleDatasource{
		im: im,
	}

	return datasource.ServeOpts{
		QueryDataHandler:   ds,
		CheckHealthHandler: ds,
	}
}

// SampleDatasource is an example datasource used to scaffold
// new datasource plugins with an backend.
type SampleDatasource struct {
	// The instance manager can help with lifecycle management
	// of datasource instances in plugins. It's not a requirements
	// but a best practice that we recommend that you follow.
	im instancemgmt.InstanceManager
}

// QueryData handles multiple queries and returns multiple responses.
// req contains the queries []DataQuery (where each query contains RefID as a unique identifer).
// The QueryDataResponse contains a map of RefID to the response for each query, and each response
// contains Frames ([]*Frame).
func (td *SampleDatasource) QueryData(ctx context.Context, req *backend.QueryDataRequest) (*backend.QueryDataResponse, error) {
    defer func() {
        if r := recover(); r != nil {
            log.DefaultLogger.Info("Recovered in QueryData", "error", r)
        }
    }()
	log.DefaultLogger.Info("QueryData", "request", req)

	instance, err := td.im.Get(req.PluginContext)
	if err != nil {
	   log.DefaultLogger.Info("Failed getting connection", "error", err)
	   return nil, err
	}
	// create response struct
	response := backend.NewQueryDataResponse()
	instSetting, ok := instance.(*instanceSettings)
    if !ok {
        log.DefaultLogger.Info("Failed getting connection")
        return nil, nil
    }
	// loop over queries and execute them individually.
	for _, q := range req.Queries {
		res := td.query(ctx, instSetting, q)

		// save the response in a hashmap
		// based on with RefID as identifier
		response.Responses[q.RefID] = res
	}

	return response, nil
}

type queryModel struct {
	Format string `json:"format"`
	QueryTxt string `json:"queryTxt"`
}

func getTypeArray(typ string) interface{} {
    log.DefaultLogger.Debug("getTypeArray", "type", typ)
    switch t := typ; t {
        case "timestamp":
            return []time.Time{}
        case "bigint", "int":
            return []int64{}
        case "smallint":
            return []int16{}
        case "boolean":
            return []bool{}
        case "double", "varint", "decimal":
            return []float64{}
        case "float":
            return []float32{}
        case "tinyint":
            return []int8{}
        default:
            return []string{}
    }
}

func toValue(val interface{}, typ string) interface{} {
    if (val == nil) {
        return nil
    }
    switch t := typ; t {
        case "blob":
            return "Blob"
    }
    switch t := val.(type) {
        case float32, time.Time, string, int64, float64, bool, int16, int8:
            return t
        case gocql.UUID:
            return t.String()
        case int:
            return int64(t)
        case *inf.Dec:
            if s, err := strconv.ParseFloat(t.String(), 64); err == nil {
                return s
            }
            return 0
        case *big.Int:
            if s, err := strconv.ParseFloat(t.String(), 64); err == nil {
                return s
            }
            return 0
        default:
            r, err := json.Marshal(val)
            if (err != nil) {
                log.DefaultLogger.Info("Marsheling failed ", "err", err)
            }
            return string(r)
    }
}

func (td *SampleDatasource) query(ctx context.Context, instance *instanceSettings,  query backend.DataQuery) backend.DataResponse {
	// Unmarshal the json into our queryModel
	var hosts queryModel

	response := backend.DataResponse{}

	response.Error = json.Unmarshal(query.JSON, &hosts)
	var v interface{}
	json.Unmarshal(query.JSON, &v)
	dt := v.(map[string]interface{})
	if response.Error != nil {
	   log.DefaultLogger.Warn("Failed unmarsheling json", "err", response.Error, "json ", string(query.JSON))
		return response
	}

	// Log a warning if `Format` is empty.
	if hosts.Format == "" {
		log.DefaultLogger.Info("format is empty. defaulting to time series")
	}

	// create data frame response
	frame := data.NewFrame("response")
	if val, ok := dt["queryText"]; ok {
	   querytxt := fmt.Sprintf("%v", val)
	   log.DefaultLogger.Debug("queryText found", "querytxt", querytxt, "instance", instance)
	   specificHost, ok := dt["queryHost"];
	   if ok {
	       log.DefaultLogger.Debug("Using host", "host", specificHost)
	   }
       session, err := instance.getSession(specificHost)
       if err != nil {
           log.DefaultLogger.Warn("Failed getting session", "err", err, "host", specificHost)
           return response
       }
	   iter := session.Query(querytxt).Iter()
	   cols := iter.Columns()
	   for _, c := range iter.Columns() {
            frame.Fields = append(frame.Fields,
                data.NewField(c.Name, nil, getTypeArray(c.TypeInfo.Type().String())),
            )
        }
        for {
            // New map each iteration
            row := make(map[string]interface{})
            if !iter.MapScan(row) {
                break
            }
            vals := make([]interface{}, len(cols))
            for i, c := range cols {
                vals[i] = toValue(row[c.Name], c.TypeInfo.Type().String())
            }
            log.DefaultLogger.Debug("adding vals", "vals", vals)
            frame.AppendRow(vals...)
        }
        if err := iter.Close(); err != nil {
            log.DefaultLogger.Warn(err.Error())
        }
    }
	// create data frame response
	// add the frames to the response
	response.Frames = append(response.Frames, frame)

	return response
}

// CheckHealth handles health checks sent from Grafana to the plugin.
// The main use case for these health checks is the test button on the
// datasource configuration page which allows users to verify that
// a datasource is working as expected.
func (td *SampleDatasource) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	var status = backend.HealthStatusOk
	var message = "Data source is working"

	return &backend.CheckHealthResult{
		Status:  status,
		Message: message,
	}, nil
}

type instanceSettings struct {
    cluster *gocql.ClusterConfig
    sessions map[string]*gocql.Session
}

func (settings *instanceSettings) getSession(hostRef interface{}) (*gocql.Session, error) {
    var host string
    if hostRef != nil {
        host = fmt.Sprintf("%v", hostRef)
    }
    if val, ok := settings.sessions[host]; ok {
        return val, nil
    }
    if host == "" {
        settings.cluster.HostFilter = nil
    } else {
        settings.cluster.HostFilter = gocql.WhiteListHostFilter(host)
    }
    session, err := gocql.NewSession(*settings.cluster)
    if err != nil {
        log.DefaultLogger.Info("unable to connect to scylla", "err", err, "session", session, "host", host)
        return nil, err
    }
    settings.sessions[host] = session
    return session, nil
}

func newDataSourceInstance(setting backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
    type editModel struct {
        Host string `json:"host"`
    }
    var hosts editModel
    log.DefaultLogger.Debug("newDataSourceInstance", "data", setting.JSONData)
    var secureData = setting.DecryptedSecureJSONData
    err := json.Unmarshal(setting.JSONData, &hosts)
    if err != nil {
        log.DefaultLogger.Warn("error marsheling", "err", err)
        return nil, err
    }
    log.DefaultLogger.Info("looking for host", "host", hosts.Host)
    var newCluster = gocql.NewCluster(hosts.Host)
    password, hasPassword := secureData["password"]
    user, hasUser := secureData["user"]
    if hasPassword && hasUser {
        log.DefaultLogger.Debug("using username and password", "user", user)
        newCluster.Authenticator = gocql.PasswordAuthenticator{
            Username: user,
            Password: password,
        }
    }

	return &instanceSettings{
		cluster: newCluster,
		sessions: make(map[string]*gocql.Session),
	}, nil
}

func (s *instanceSettings) Dispose() {
	// Called before creatinga a new instance to allow plugin authors
	// to cleanup.
}
