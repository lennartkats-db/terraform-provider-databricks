package compute

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/databrickslabs/terraform-provider-databricks/common"
	"github.com/databrickslabs/terraform-provider-databricks/qa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreateRunningCluster_AzureAuth(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/list",
			Response: map[string]interface{}{},
		},
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/spark-versions",
			Response: SparkVersionsList{
				SparkVersions: []SparkVersion{
					{
						Version:     "7.1.x-cpu-ml-scala2.12",
						Description: "7.1 ML (includes Apache Spark 3.0.0, Scala 2.12)",
					},
					{
						Version:     "apache-spark-2.4.x-scala2.11",
						Description: "Light 2.4 (includes Apache Spark 2.4, Scala 2.11)",
					},
					{
						Version:     "7.3.x-scala2.12",
						Description: "7.3 LTS (includes Apache Spark 3.0.1, Scala 2.12)",
					},
					{
						Version:     "6.4.x-scala2.11",
						Description: "6.4 (includes Apache Spark 2.4.5, Scala 2.11)",
					},
				},
			},
		},
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/list-node-types",
			Response: NodeTypeList{
				[]NodeType{
					{
						NodeTypeID:     "Standard_F4s",
						InstanceTypeID: "Standard_F4s",
						MemoryMB:       8192,
						NumCores:       4,
						NodeInstanceType: &NodeInstanceType{
							LocalDisks:      1,
							InstanceTypeID:  "Standard_F4s",
							LocalDiskSizeGB: 16,
							LocalNVMeDisks:  0,
						},
					},
					{
						NodeTypeID:     "Standard_L80s_v2",
						InstanceTypeID: "Standard_L80s_v2",
						MemoryMB:       655360,
						NumCores:       80,
						NodeInstanceType: &NodeInstanceType{
							LocalDisks:      2,
							InstanceTypeID:  "Standard_L80s_v2",
							LocalDiskSizeGB: 160,
							LocalNVMeDisks:  1,
						},
					},
				},
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/create",
			ExpectedRequest: Cluster{
				AutoterminationMinutes: 10,
				ClusterName:            "mount",
				NodeTypeID:             "Standard_F4s",
				NumWorkers:             1,
				SparkVersion:           "7.3.x-scala2.12",
			},
			Response: ClusterID{
				ClusterID: "bcd",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=bcd",
			Response: ClusterInfo{
				State: "RUNNING",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	client.AzureDatabricksResourceID = "/subscriptions/a/resourceGroups/b/providers/Microsoft.Databricks/workspaces/c"

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).GetOrCreateRunningCluster("mount")
	require.NoError(t, err)

	assert.NotNil(t, clusterInfo)
}

func TestGetOrCreateRunningCluster_Existing_AzureAuth(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/list",
			Response: ClusterList{
				Clusters: []ClusterInfo{
					{
						ClusterID:              "abc",
						State:                  "TERMINATED",
						AutoterminationMinutes: 10,
						ClusterName:            "mount",
						NodeTypeID:             "Standard_F4s",
						NumWorkers:             1,
						SparkVersion:           "7.3.x-scala2.12",
					},
				},
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: "TERMINATED",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/start",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: "RUNNING",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	client.AzureDatabricksResourceID = "/a/b/c"

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).GetOrCreateRunningCluster("mount")
	require.NoError(t, err)

	assert.NotNil(t, clusterInfo)
}

func TestWaitForClusterStatus_RetryOnNotFound(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: common.APIErrorBody{
				Message: "Nope",
			},
			Status: 404,
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: "RUNNING",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	client.AzureDatabricksResourceID = "/a/b/c"

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).waitForClusterStatus("abc", ClusterStateRunning)
	require.NoError(t, err)

	assert.NotNil(t, clusterInfo)
}

func TestWaitForClusterStatus_StopRetryingEarly(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: common.APIErrorBody{
				Message: "I am a teapot",
			},
			Status: 418,
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = NewClustersAPI(ctx, client).waitForClusterStatus("abc", ClusterStateRunning)
	require.Error(t, err)
	require.Contains(t, err.Error(), "I am a teapot")
}

func TestWaitForClusterStatus_NotReachable(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:        ClusterStateUnknown,
				StateMessage: "Something strange is going on",
				TerminationReason: &TerminationReason{Code: "unknown", Type: "broken",
					Parameters: map[string]string{"abc": "def"}},
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	client.AzureDatabricksResourceID = "/a/b/c"

	ctx := context.Background()
	_, err = NewClustersAPI(ctx, client).waitForClusterStatus("abc", ClusterStateRunning)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "abc is not able to transition from UNKNOWN to RUNNING: Something strange is going on")
	assert.Contains(t, err.Error(), "code: unknown, type: broken")
}

func TestWaitForClusterStatus_NormalRetry(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: ClusterStatePending,
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: ClusterStateRunning,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).waitForClusterStatus("abc", ClusterStateRunning)
	require.NoError(t, err)
	assert.Equal(t, ClusterStateRunning, string(clusterInfo.State))
}

func TestEditCluster_Pending(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStatePending,
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateRunning,
				ClusterID: "abc",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/edit",
			Response: Cluster{
				ClusterID:   "abc",
				ClusterName: "Morty",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: ClusterStateRunning,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).Edit(Cluster{
		ClusterID:   "abc",
		ClusterName: "Morty",
	})
	require.NoError(t, err)
	assert.Equal(t, ClusterStateRunning, string(clusterInfo.State))
}

func TestEditCluster_Terminating(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateTerminating,
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateTerminated,
				ClusterID: "abc",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/edit",
			Response: Cluster{
				ClusterID:   "abc",
				ClusterName: "Morty",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: ClusterStateRunning,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).Edit(Cluster{
		ClusterID:   "abc",
		ClusterName: "Morty",
	})
	require.NoError(t, err)
	assert.Equal(t, ClusterStateTerminated, string(clusterInfo.State))
}

func TestEditCluster_Error(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:        ClusterStateError,
				ClusterID:    "abc",
				StateMessage: "I am a teapot",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = NewClustersAPI(ctx, client).Edit(Cluster{
		ClusterID:   "abc",
		ClusterName: "Morty",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "I am a teapot")
}

func TestStartAndGetInfo_Pending(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStatePending,
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateRunning,
				ClusterID: "abc",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).StartAndGetInfo("abc")
	require.NoError(t, err)
	assert.Equal(t, ClusterStateRunning, string(clusterInfo.State))
}

func TestStartAndGetInfo_Terminating(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateTerminating,
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateTerminated,
				ClusterID: "abc",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/start",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateRunning,
				ClusterID: "abc",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).StartAndGetInfo("abc")
	require.NoError(t, err)
	assert.Equal(t, ClusterStateRunning, string(clusterInfo.State))
}

func TestStartAndGetInfo_Error(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:        ClusterStateError,
				StateMessage: "I am a teapot",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/start",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:     ClusterStateRunning,
				ClusterID: "abc",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterInfo, err := NewClustersAPI(ctx, client).StartAndGetInfo("abc")
	require.NoError(t, err)
	assert.Equal(t, ClusterStateRunning, string(clusterInfo.State))
}

func TestStartAndGetInfo_StartingError(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State:        ClusterStateError,
				StateMessage: "I am a teapot",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/start",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
			Response: common.APIErrorBody{
				Message: "I am a teapot!",
			},
			Status: 418,
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = NewClustersAPI(ctx, client).StartAndGetInfo("abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "I am a teapot")
}

func TestPermanentDelete_Pinned(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/delete",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
		},
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/get?cluster_id=abc",
			Response: ClusterInfo{
				State: ClusterStateTerminated,
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/permanent-delete",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
			Response: common.APIErrorBody{
				Message: "unpin the cluster first",
			},
			Status: 400,
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/unpin",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/permanent-delete",
			ExpectedRequest: ClusterID{
				ClusterID: "abc",
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	err = NewClustersAPI(ctx, client).PermanentDelete("abc")
	require.NoError(t, err)
}

func TestAccListClustersIntegration(t *testing.T) {
	cloudEnv := os.Getenv("CLOUD_ENV")
	if cloudEnv == "" {
		t.Skip("Acceptance tests skipped unless env 'CLOUD_ENV' is set")
	}

	client := common.CommonEnvironmentClient()
	ctx := context.Background()
	clustersAPI := NewClustersAPI(ctx, client)
	randomName := qa.RandomName()

	cluster := Cluster{
		NumWorkers:             1,
		ClusterName:            "Terraform Integration Test " + randomName,
		SparkVersion:           clustersAPI.LatestSparkVersionOrDefault(SparkVersionRequest{Latest: true, LongTermSupport: true}),
		InstancePoolID:         CommonInstancePoolID(),
		IdempotencyToken:       "acc-list-" + randomName,
		AutoterminationMinutes: 15,
	}
	clusterReadInfo, err := clustersAPI.Create(cluster)
	assert.NoError(t, err, err)
	assert.True(t, clusterReadInfo.NumWorkers == cluster.NumWorkers)
	assert.True(t, clusterReadInfo.ClusterName == cluster.ClusterName)
	assert.True(t, reflect.DeepEqual(clusterReadInfo.SparkEnvVars, cluster.SparkEnvVars))
	assert.True(t, clusterReadInfo.SparkVersion == cluster.SparkVersion)
	assert.True(t, clusterReadInfo.AutoterminationMinutes == cluster.AutoterminationMinutes)
	assert.True(t, clusterReadInfo.State == ClusterStateRunning)

	defer func() {
		err = NewClustersAPI(ctx, client).Terminate(clusterReadInfo.ClusterID)
		assert.NoError(t, err, err)

		clusterReadInfo, err = NewClustersAPI(ctx, client).Get(clusterReadInfo.ClusterID)
		assert.NoError(t, err, err)
		assert.True(t, clusterReadInfo.State == ClusterStateTerminated)

		err = NewClustersAPI(ctx, client).Unpin(clusterReadInfo.ClusterID)
		assert.NoError(t, err, err)

		err = NewClustersAPI(ctx, client).PermanentDelete(clusterReadInfo.ClusterID)
		assert.NoError(t, err, err)
	}()

	err = NewClustersAPI(ctx, client).Pin(clusterReadInfo.ClusterID)
	assert.NoError(t, err, err)

	clusterReadInfo, err = NewClustersAPI(ctx, client).Get(clusterReadInfo.ClusterID)
	assert.NoError(t, err, err)
	assert.True(t, clusterReadInfo.State == ClusterStateRunning)
}

func TestAwsAccSmallestNodeType(t *testing.T) {
	cloudEnv := os.Getenv("CLOUD_ENV")
	if cloudEnv == "" {
		t.Skip("Acceptance tests skipped unless env 'CLOUD_ENV' is set")
	}

	client := common.CommonEnvironmentClient()
	ctx := context.Background()
	nodeType := NewClustersAPI(ctx, client).GetSmallestNodeType(NodeTypeRequest{
		LocalDisk: true,
	})
	assert.Equal(t, "m5d.large", nodeType)
}

func TestAzureAccNodeTypes(t *testing.T) {
	cloudEnv := os.Getenv("CLOUD_ENV")
	if cloudEnv == "" {
		t.Skip("Acceptance tests skipped unless env 'CLOUD_ENV' is set")
	}

	ctx := context.Background()
	clustersAPI := NewClustersAPI(ctx, common.CommonEnvironmentClient())
	m := map[string]NodeTypeRequest{
		"Standard_E4s_v4":  {},
		"Standard_E32s_v4": {MinCores: 32, GBPerCore: 8},
	}

	for k, v := range m {
		assert.Equal(t, k, clustersAPI.GetSmallestNodeType(v))
	}
}

func TestEventsSinglePage(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(123),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 1,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterEvents, err := NewClustersAPI(ctx, client).Events(EventsRequest{ClusterID: "abc"})
	require.NoError(t, err)
	assert.Equal(t, len(clusterEvents), 1)
	assert.Equal(t, clusterEvents[0].ClusterID, "abc")
	assert.Equal(t, clusterEvents[0].Timestamp, int64(123))
	assert.Equal(t, clusterEvents[0].Type, EvTypeRunning)
	assert.Equal(t, clusterEvents[0].Details.CurrentNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[0].Details.TargetNumWorkers, int32(2))
}

func TestEventsTwoPages(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(123),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 2,
				NextPage: &EventsRequest{
					ClusterID: "abc",
					Offset:    1,
				},
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
				Offset:    1,
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(124),
						Type:      EvTypeTerminating,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 2,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterEvents, err := NewClustersAPI(ctx, client).Events(EventsRequest{ClusterID: "abc"})
	require.NoError(t, err)
	assert.Equal(t, len(clusterEvents), 2)
	assert.Equal(t, clusterEvents[0].ClusterID, "abc")
	assert.Equal(t, clusterEvents[0].Timestamp, int64(123))
	assert.Equal(t, clusterEvents[0].Type, EvTypeRunning)
	assert.Equal(t, clusterEvents[0].Details.CurrentNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[0].Details.TargetNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[1].ClusterID, "abc")
	assert.Equal(t, clusterEvents[1].Timestamp, int64(124))
	assert.Equal(t, clusterEvents[1].Type, EvTypeTerminating)
	assert.Equal(t, clusterEvents[1].Details.CurrentNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[1].Details.TargetNumWorkers, int32(2))
}

func TestEventsTwoPagesMaxItems(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
				Limit:     1,
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(123),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 2,
				NextPage: &EventsRequest{
					ClusterID: "abc",
					Offset:    1,
				},
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterEvents, err := NewClustersAPI(ctx, client).Events(EventsRequest{ClusterID: "abc", MaxItems: 1, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, len(clusterEvents), 1)
	assert.Equal(t, clusterEvents[0].ClusterID, "abc")
	assert.Equal(t, clusterEvents[0].Timestamp, int64(123))
	assert.Equal(t, clusterEvents[0].Type, EvTypeRunning)
	assert.Equal(t, clusterEvents[0].Details.CurrentNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[0].Details.TargetNumWorkers, int32(2))
}

func TestEventsTwoPagesMaxThreeItems(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
				Limit:     2,
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(123),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
					{
						ClusterID: "abc",
						Timestamp: int64(124),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 4,
				NextPage: &EventsRequest{
					ClusterID: "abc",
					Offset:    2,
				},
			},
		},
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
				Offset:    2,
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(125),
						Type:      EvTypeTerminating,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
					{
						ClusterID: "abc",
						Timestamp: int64(126),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 4,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterEvents, err := NewClustersAPI(ctx, client).Events(EventsRequest{ClusterID: "abc", MaxItems: 3, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, len(clusterEvents), 3)
	assert.Equal(t, clusterEvents[0].ClusterID, "abc")
	assert.Equal(t, clusterEvents[0].Timestamp, int64(123))
	assert.Equal(t, clusterEvents[0].Type, EvTypeRunning)
	assert.Equal(t, clusterEvents[0].Details.CurrentNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[0].Details.TargetNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[1].Timestamp, int64(124))
	assert.Equal(t, clusterEvents[2].Timestamp, int64(125))
}

func TestEventsTwoPagesNoNextPage(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
				Limit:     2,
			},
			Response: EventsResponse{
				Events: []ClusterEvent{
					{
						ClusterID: "abc",
						Timestamp: int64(123),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
					{
						ClusterID: "abc",
						Timestamp: int64(124),
						Type:      EvTypeRunning,
						Details: EventDetails{
							CurrentNumWorkers: int32(2),
							TargetNumWorkers:  int32(2),
						},
					},
				},
				TotalCount: 4,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterEvents, err := NewClustersAPI(ctx, client).Events(EventsRequest{ClusterID: "abc", MaxItems: 3, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, len(clusterEvents), 2)
	assert.Equal(t, clusterEvents[0].ClusterID, "abc")
	assert.Equal(t, clusterEvents[0].Timestamp, int64(123))
	assert.Equal(t, clusterEvents[0].Type, EvTypeRunning)
	assert.Equal(t, clusterEvents[0].Details.CurrentNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[0].Details.TargetNumWorkers, int32(2))
	assert.Equal(t, clusterEvents[1].Timestamp, int64(124))
}

func TestEventsEmptyResult(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "POST",
			Resource: "/api/2.0/clusters/events",
			ExpectedRequest: EventsRequest{
				ClusterID: "abc",
				Limit:     2,
			},
			Response: EventsResponse{
				Events:     []ClusterEvent{},
				TotalCount: 0,
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	clusterEvents, err := NewClustersAPI(ctx, client).Events(EventsRequest{ClusterID: "abc", MaxItems: 3, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, len(clusterEvents), 0)
}

func TestListSparkVersions(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/spark-versions",
			Response: SparkVersionsList{
				SparkVersions: []SparkVersion{
					{
						Version:     "7.1.x-cpu-ml-scala2.12",
						Description: "7.1 ML (includes Apache Spark 3.0.0, Scala 2.12)",
					},
					{
						Version:     "apache-spark-2.4.x-scala2.11",
						Description: "Light 2.4 (includes Apache Spark 2.4, Scala 2.11)",
					},
					{
						Version:     "7.3.x-hls-scala2.12",
						Description: "7.3 LTS Genomics (includes Apache Spark 3.0.1, Scala 2.12)",
					},
					{
						Version:     "6.4.x-scala2.11",
						Description: "6.4 (includes Apache Spark 2.4.5, Scala 2.11)",
					},
				},
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	sparkVersions, err := NewClustersAPI(ctx, client).ListSparkVersions()
	require.NoError(t, err)
	require.Equal(t, 4, len(sparkVersions.SparkVersions))
	require.Equal(t, "6.4.x-scala2.11", sparkVersions.SparkVersions[3].Version)
}

func TestListSparkVersionsWithError(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:   "GET",
			Resource: "/api/2.0/clusters/spark-versions",
			Response: "{garbage....",
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	_, err = NewClustersAPI(ctx, client).ListSparkVersions()
	require.Error(t, err)
	require.Equal(t, true, strings.Contains(err.Error(), "Invalid JSON received"))
}

func TestGetLatestSparkVersion(t *testing.T) {
	versions := SparkVersionsList{
		SparkVersions: []SparkVersion{
			{
				Version:     "7.1.x-cpu-ml-scala2.12",
				Description: "7.1 ML (includes Apache Spark 3.0.0, Scala 2.12)",
			},
			{
				Version:     "apache-spark-2.4.x-scala2.11",
				Description: "Light 2.4 (includes Apache Spark 2.4, Scala 2.11)",
			},
			{
				Version:     "7.3.x-hls-scala2.12",
				Description: "7.3 LTS Genomics (includes Apache Spark 3.0.1, Scala 2.12)",
			},
			{
				Version:     "6.4.x-scala2.11",
				Description: "6.4 (includes Apache Spark 2.4.5, Scala 2.11)",
			},
			{
				Version:     "7.3.x-scala2.12",
				Description: "7.3 LTS (includes Apache Spark 3.0.1, Scala 2.12)",
			},
			{
				Version:     "7.4.x-scala2.12",
				Description: "7.4 (includes Apache Spark 3.0.1, Scala 2.12)",
			},
			{
				Version:     "7.1.x-scala2.12",
				Description: "7.1 (includes Apache Spark 3.0.0, Scala 2.12)",
			},
		},
	}

	version, err := versions.LatestSparkVersion(SparkVersionRequest{Scala: "2.12", Latest: true})
	require.NoError(t, err)
	require.Equal(t, "7.4.x-scala2.12", version)

	version, err = versions.LatestSparkVersion(SparkVersionRequest{Scala: "2.12", LongTermSupport: true, Latest: true})
	require.NoError(t, err)
	require.Equal(t, "7.3.x-scala2.12", version)

	version, err = versions.LatestSparkVersion(SparkVersionRequest{Scala: "2.12", Latest: true, SparkVersion: "3.0.0"})
	require.NoError(t, err)
	require.Equal(t, "7.1.x-scala2.12", version)

	_, err = versions.LatestSparkVersion(SparkVersionRequest{Scala: "2.12"})
	require.Error(t, err)
	require.Equal(t, true, strings.Contains(err.Error(), "query returned multiple results"))

	_, err = versions.LatestSparkVersion(SparkVersionRequest{Scala: "2.12", ML: true, Genomics: true})
	require.Error(t, err)
	require.Equal(t, true, strings.Contains(err.Error(), "query returned no results"))

	_, err = versions.LatestSparkVersion(SparkVersionRequest{Scala: "2.12", SparkVersion: "3.10"})
	require.Error(t, err)
	require.Equal(t, true, strings.Contains(err.Error(), "query returned no results"))
}

func TestListNodeTypes(t *testing.T) {
	client, server, err := qa.HttpFixtureClient(t, []qa.HTTPFixture{
		{
			Method:       "GET",
			ReuseRequest: true,
			Resource:     "/api/2.0/clusters/list-node-types",
			Response: NodeTypeList{
				[]NodeType{
					{
						NodeTypeID:     "Standard_F4s",
						InstanceTypeID: "Standard_F4s",
						MemoryMB:       8192,
						NumCores:       4,
						NodeInstanceType: &NodeInstanceType{
							LocalDisks:      1,
							InstanceTypeID:  "Standard_F4s",
							LocalDiskSizeGB: 16,
							LocalNVMeDisks:  0,
						},
					},
					{
						NodeTypeID:     "Standard_L80s_v2",
						InstanceTypeID: "Standard_L80s_v2",
						MemoryMB:       655360,
						NumCores:       80,
						NodeInstanceType: &NodeInstanceType{
							LocalDisks:      2,
							InstanceTypeID:  "Standard_L80s_v2",
							LocalDiskSizeGB: 160,
							LocalNVMeDisks:  1,
						},
					},
				},
			},
		},
	})
	defer server.Close()
	require.NoError(t, err)

	ctx := context.Background()
	api := NewClustersAPI(ctx, client)
	nodeType := api.GetSmallestNodeType(NodeTypeRequest{SupportPortForwarding: true})
	assert.Equal(t, nodeType, defaultSmallestNodeType(api))
	nodeType = api.GetSmallestNodeType(NodeTypeRequest{PhotonWorkerCapable: true})
	assert.Equal(t, nodeType, defaultSmallestNodeType(api))
	nodeType = api.GetSmallestNodeType(NodeTypeRequest{PhotonDriverCapable: true})
	assert.Equal(t, nodeType, defaultSmallestNodeType(api))
	nodeType = api.GetSmallestNodeType(NodeTypeRequest{IsIOCacheEnabled: true})
	assert.Equal(t, nodeType, defaultSmallestNodeType(api))
	nodeType = api.GetSmallestNodeType(NodeTypeRequest{Category: "Storage Optimized"})
	assert.Equal(t, nodeType, defaultSmallestNodeType(api))
}
