package nsm

import (
	"context"
	"testing"
	"time"

	nsm_api "github.com/networkservicemesh/networkservicemesh/controlplane/api/nsm"

	"github.com/golang/protobuf/ptypes/empty"

	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/common"

	"github.com/gogo/protobuf/proto"
	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	net_context "golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/networkservicemesh/controlplane/api/crossconnect"
	local_connection "github.com/networkservicemesh/networkservicemesh/controlplane/api/local/connection"
	local_networkservice "github.com/networkservicemesh/networkservicemesh/controlplane/api/local/networkservice"
	"github.com/networkservicemesh/networkservicemesh/controlplane/api/nsm/connection"
	"github.com/networkservicemesh/networkservicemesh/controlplane/api/nsm/networkservice"
	"github.com/networkservicemesh/networkservicemesh/controlplane/api/registry"
	remote_connection "github.com/networkservicemesh/networkservicemesh/controlplane/api/remote/connection"
	remote_networkservice "github.com/networkservicemesh/networkservicemesh/controlplane/api/remote/networkservice"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/api/nsm"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/model"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/nsmd"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/serviceregistry"
	test_utils "github.com/networkservicemesh/networkservicemesh/controlplane/pkg/tests/utils"
)

const (
	networkServiceName = "golden_network"

	localNSMName  = "nsm-local"
	remoteNSMName = "nsm-remote"

	dataplane1Name = "dataplane-1"

	nse1Name = "nse-1"
	nse2Name = "nse-2"
)

type healTestData struct {
	model model.Model

	serviceRegistry *serviceRegistryStub
	nseManager      *nseManagerStub

	healProcessor     *healProcessor
	connectionManager *connectionManagerStub
}

type localManagerManagerStub struct {
	connectionManager *connectionManagerStub
}

func (mgr *localManagerManagerStub) Request(ctx context.Context, request *local_networkservice.NetworkServiceRequest) (*local_connection.Connection, error) {
	conn, err := mgr.connectionManager.request(ctx, request, mgr.connectionManager.model.GetClientConnection(request.GetConnection().GetId()))
	if err == nil {
		return conn.(*local_connection.Connection), nil
	}
	return nil, err
}

func (mgr *localManagerManagerStub) Close(ctx context.Context, connection *local_connection.Connection) (*empty.Empty, error) {
	err := mgr.connectionManager.Close(ctx, mgr.connectionManager.model.GetClientConnection(connection.GetId()))
	return &empty.Empty{}, err
}

type remoteManagerManagerStub struct {
	connectionManager *connectionManagerStub
}

func (mgr *remoteManagerManagerStub) Request(ctx context.Context, request *remote_networkservice.NetworkServiceRequest) (*remote_connection.Connection, error) {
	conn, err := mgr.connectionManager.request(ctx, request, mgr.connectionManager.model.GetClientConnection(request.GetConnection().GetId()))
	if err != nil {
		return conn.(*remote_connection.Connection), nil
	}
	return nil, err
}

func (mgr *remoteManagerManagerStub) Close(ctx context.Context, connection *remote_connection.Connection) (*empty.Empty, error) {
	err := mgr.connectionManager.Close(ctx, mgr.connectionManager.model.GetClientConnection(connection.GetId()))
	return &empty.Empty{}, err
}

func newHealTestData() *healTestData {
	var data = &healTestData{
		model: model.NewModel(),
	}

	data.serviceRegistry = &serviceRegistryStub{
		discoveryClient: &discoveryClientStub{
			response: data.createFindNetworkServiceResponse(),
		},
	}
	data.connectionManager = &connectionManagerStub{
		model: data.model,
	}
	data.nseManager = &nseManagerStub{
		model: data.model,

		nseClients: map[string]*nseClientStub{},
		nses:       []*registry.NSERegistration{},
	}

	data.healProcessor = &healProcessor{
		serviceRegistry: data.serviceRegistry,
		model:           data.model,
		properties: &nsm_api.Properties{
			HealEnabled:               true,
			HealRetryCount:            1,
			HealRequestConnectTimeout: 15 * time.Second,
		},
		nseManager: data.nseManager,
		manager:    data.connectionManager,
	}

	data.model.SetNsm(&registry.NetworkServiceManager{
		Name: localNSMName,
	})
	data.model.AddDataplane(context.Background(), &model.Dataplane{
		RegisteredName:       dataplane1Name,
		MechanismsConfigured: true,
	})

	return data
}

func TestHealDstDown_RemoteClientLocalEndpoint(t *testing.T) {
	g := NewWithT(t)
	data := newHealTestData()

	nse1 := data.createEndpoint(nse1Name, localNSMName)
	data.model.AddEndpoint(context.Background(), &model.Endpoint{
		Endpoint: nse1,
	})

	xcon := data.createCrossConnection(true, false, "src", "dst")
	request := data.createRequest(true)
	connection := data.createClientConnection("id", xcon, nse1, remoteNSMName, dataplane1Name, request)
	data.model.AddClientConnection(context.Background(), connection)

	healed := data.healProcessor.healDstDown(context.Background(), data.cloneClientConnection(connection))
	g.Expect(healed).To(BeFalse())

	test_utils.NewModelVerifier(data.model).
		EndpointExists(nse1Name, localNSMName).
		ClientConnectionExists("id", "src", "dst", remoteNSMName, nse1Name, dataplane1Name).
		DataplaneExists(dataplane1Name).
		Verify(t)
}

func TestHealDstDown_LocalClientLocalEndpoint(t *testing.T) {
	g := NewWithT(t)
	data := newHealTestData()

	nse1 := data.createEndpoint(nse1Name, localNSMName)

	nse2 := data.createEndpoint(nse2Name, localNSMName)
	data.model.AddEndpoint(context.Background(), &model.Endpoint{
		Endpoint: nse2,
	})

	xcon := data.createCrossConnection(false, false, "src", "dst")
	request := data.createRequest(false)
	connection := data.createClientConnection("id", xcon, nse1, localNSMName, dataplane1Name, request)
	data.model.AddClientConnection(context.Background(), connection)

	data.serviceRegistry.discoveryClient.response = data.createFindNetworkServiceResponse(nse2)
	data.connectionManager.nse = nse2

	healed := data.healProcessor.healDstDown(context.Background(), data.cloneClientConnection(connection))
	g.Expect(healed).To(BeTrue())

	test_utils.NewModelVerifier(data.model).
		EndpointNotExists(nse1Name).
		EndpointExists(nse2Name, localNSMName).
		ClientConnectionExists("id", "src", "dst", localNSMName, nse2Name, dataplane1Name).
		DataplaneExists(dataplane1Name).
		Verify(t)
}

func TestHealDstDown_LocalClientLocalEndpoint_NoNSEFound(t *testing.T) {
	g := NewWithT(t)
	data := newHealTestData()

	nse1 := data.createEndpoint(nse1Name, localNSMName)
	data.model.AddEndpoint(context.Background(), &model.Endpoint{
		Endpoint: nse1,
	})

	xcon := data.createCrossConnection(false, false, "src", "dst")
	request := data.createRequest(false)
	connection := data.createClientConnection("id", xcon, nse1, localNSMName, dataplane1Name, request)
	data.model.AddClientConnection(context.Background(), connection)

	healed := data.healProcessor.healDstDown(context.Background(), data.cloneClientConnection(connection))
	g.Expect(healed).To(BeFalse())

	test_utils.NewModelVerifier(data.model).
		EndpointExists(nse1Name, localNSMName).
		ClientConnectionNotExists("id").
		DataplaneExists(dataplane1Name).
		Verify(t)
}

func TestHealDstDown_LocalClientLocalEndpoint_RequestFailed(t *testing.T) {
	g := NewWithT(t)
	data := newHealTestData()

	nse1 := data.createEndpoint(nse1Name, localNSMName)
	data.model.AddEndpoint(context.Background(), &model.Endpoint{
		Endpoint: nse1,
	})

	xcon := data.createCrossConnection(false, false, "src", "dst")
	request := data.createRequest(false)
	connection := data.createClientConnection("id", xcon, nse1, localNSMName, dataplane1Name, request)
	data.model.AddClientConnection(context.Background(), connection)

	data.connectionManager.requestError = errors.New("request error")

	healed := data.healProcessor.healDstDown(context.Background(), data.cloneClientConnection(connection))
	g.Expect(healed).To(BeFalse())

	test_utils.NewModelVerifier(data.model).
		EndpointExists(nse1Name, localNSMName).
		DataplaneExists(dataplane1Name).
		Verify(t)
}

func TestHealDstDown_LocalClientRemoteEndpoint(t *testing.T) {
	g := NewWithT(t)
	data := newHealTestData()

	nse1 := data.createEndpoint(nse1Name, remoteNSMName)

	nse2 := data.createEndpoint(nse2Name, remoteNSMName)
	data.nseManager.nses = append(data.nseManager.nses, nse2)

	xcon := data.createCrossConnection(false, true, "src", "dst")
	request := data.createRequest(false)
	connection := data.createClientConnection("id", xcon, nse1, remoteNSMName, dataplane1Name, request)
	data.model.AddClientConnection(context.Background(), connection)

	data.serviceRegistry.discoveryClient.response = data.createFindNetworkServiceResponse(nse2)
	data.connectionManager.nse = nse2

	healed := data.healProcessor.healDstDown(context.Background(), data.cloneClientConnection(connection))
	g.Expect(healed).To(BeTrue())

	g.Expect(data.nseManager.nseClients[nse1Name].cleanedUp).To(BeTrue())
	g.Expect(data.nseManager.nseClients[nse2Name]).To(BeNil())

	test_utils.NewModelVerifier(data.model).
		EndpointNotExists(nse1Name).
		EndpointNotExists(nse2Name).
		ClientConnectionExists("id", "src", "dst", remoteNSMName, nse2Name, dataplane1Name).
		DataplaneExists(dataplane1Name).
		Verify(t)
}

func TestHealDstDown_LocalClientRemoteEndpoint_NoNSEFound(t *testing.T) {
	g := NewWithT(t)
	data := newHealTestData()

	nse1 := data.createEndpoint(nse1Name, remoteNSMName)

	xcon := data.createCrossConnection(false, true, "src", "dst")
	request := data.createRequest(false)
	connection := data.createClientConnection("id", xcon, nse1, remoteNSMName, dataplane1Name, request)
	data.model.AddClientConnection(context.Background(), connection)

	healed := data.healProcessor.healDstDown(context.Background(), data.cloneClientConnection(connection))
	g.Expect(healed).To(BeFalse())

	g.Expect(data.nseManager.nseClients[nse1Name].cleanedUp).To(BeTrue())

	test_utils.NewModelVerifier(data.model).
		EndpointNotExists(nse1Name).
		ClientConnectionNotExists("id").
		DataplaneExists(dataplane1Name).
		Verify(t)
}

type discoveryClientStub struct {
	response *registry.FindNetworkServiceResponse
	error    error
}

func (stub *discoveryClientStub) FindNetworkService(ctx net_context.Context, in *registry.FindNetworkServiceRequest, opts ...grpc.CallOption) (*registry.FindNetworkServiceResponse, error) {
	if in.GetNetworkServiceName() != networkServiceName {
		return nil, errors.New("wrong Network Service name")
	}
	return stub.response, stub.error
}

type serviceRegistryStub struct {
	discoveryClient *discoveryClientStub
	error           error

	serviceregistry.ServiceRegistry
}

func (stub *serviceRegistryStub) DiscoveryClient(ctx context.Context) (registry.NetworkServiceDiscoveryClient, error) {
	return stub.discoveryClient, stub.error
}

func (stub *serviceRegistryStub) WaitForDataplaneAvailable(ctx context.Context, model model.Model, timeout time.Duration) error {
	return nsmd.NewServiceRegistry().WaitForDataplaneAvailable(ctx, model, timeout)
}

type connectionManagerStub struct {
	model model.Model

	requestError error
	nse          *registry.NSERegistration

	closeError error
}

func (stub *connectionManagerStub) LocalManager(cc nsm.ClientConnection) local_networkservice.NetworkServiceServer {
	return &localManagerManagerStub{
		connectionManager: stub,
	}
}

func (stub *connectionManagerStub) RemoteManager() remote_networkservice.NetworkServiceServer {
	return &remoteManagerManagerStub{
		connectionManager: stub,
	}
}

func (stub *connectionManagerStub) request(ctx context.Context, request networkservice.Request, existingConnection *model.ClientConnection) (connection.Connection, error) {
	if stub.requestError != nil {
		return nil, stub.requestError
	}

	nsmConnection := request.GetRequestConnection().Clone()
	endpointId := registry.EndpointNSMName("")
	if existingConnection.Endpoint != nil {
		endpointId = existingConnection.Endpoint.GetEndpointNSMName()
	}

	// Update Endpoint, if less what expected
	ignoreEndpoints := common.IgnoredEndpoints(ctx)

	if ignoreEndpoints[endpointId] != nil {
		if stub.nse != nil {
			existingConnection.Endpoint = stub.nse
		} else {
			stub.model.DeleteClientConnection(context.Background(), existingConnection.GetID())
			return nil, errors.New("no NSE available")
		}
	} else {
		if stub.nse != nil {
			existingConnection.Endpoint = stub.nse
		}
	}

	existingConnection.ConnectionState = model.ClientConnectionReady
	existingConnection.DataplaneState = model.DataplaneStateReady
	stub.model.UpdateClientConnection(context.Background(), existingConnection)

	return nsmConnection, nil
}

func (stub *connectionManagerStub) Close(ctx context.Context, clientConnection nsm.ClientConnection) error {
	if stub.closeError != nil {
		return stub.closeError
	}

	cc := clientConnection.(*model.ClientConnection)

	stub.model.ApplyClientConnectionChanges(context.Background(), cc.GetID(), func(connection *model.ClientConnection) {
		connection.ConnectionState = model.ClientConnectionClosing
	})

	stub.model.DeleteEndpoint(context.Background(), cc.Endpoint.GetNetworkServiceEndpoint().GetName())
	stub.model.DeleteDataplane(context.Background(), cc.DataplaneRegisteredName)
	stub.model.DeleteClientConnection(context.Background(), cc.GetID())

	return nil
}

type nseClientStub struct {
	cleanedUp bool

	nsm.NetworkServiceClient
}

func (stub *nseClientStub) Cleanup() error {
	stub.cleanedUp = true
	return nil
}

type nseManagerStub struct {
	model model.Model

	clientError error
	nseClients  map[string]*nseClientStub

	nses []*registry.NSERegistration
}

func (stub *nseManagerStub) GetEndpoint(ctx net_context.Context, requestConnection connection.Connection, ignoreEndpoints map[registry.EndpointNSMName]*registry.NSERegistration) (*registry.NSERegistration, error) {
	panic("implement me")
}

func (stub *nseManagerStub) CreateNSEClient(ctx context.Context, endpoint *registry.NSERegistration) (nsm.NetworkServiceClient, error) {
	if stub.clientError != nil {
		return nil, stub.clientError
	}

	nseClient := &nseClientStub{
		cleanedUp: false,
	}
	stub.nseClients[endpoint.GetNetworkServiceEndpoint().GetName()] = nseClient

	return nseClient, nil
}

func (stub *nseManagerStub) IsLocalEndpoint(endpoint *registry.NSERegistration) bool {
	return stub.model.GetNsm().GetName() == endpoint.GetNetworkServiceEndpoint().GetNetworkServiceManagerName()
}

func (stub *nseManagerStub) CheckUpdateNSE(ctx context.Context, reg *registry.NSERegistration) bool {
	for _, nse := range stub.nses {
		if nse.GetNetworkServiceEndpoint().GetName() == reg.GetNetworkServiceEndpoint().GetName() {
			return true
		}
	}

	return false
}

func (data *healTestData) createEndpoint(nse, nsm string) *registry.NSERegistration {
	return &registry.NSERegistration{
		NetworkService: &registry.NetworkService{
			Name: networkServiceName,
		},
		NetworkServiceManager: &registry.NetworkServiceManager{
			Name: nsm,
		},
		NetworkServiceEndpoint: &registry.NetworkServiceEndpoint{
			Name:                      nse,
			NetworkServiceName:        networkServiceName,
			NetworkServiceManagerName: nsm,
		},
	}
}

func (data *healTestData) createCrossConnection(isRemoteSrc, isRemoteDst bool, srcID, dstID string) *crossconnect.CrossConnect {
	xcon := &crossconnect.CrossConnect{}

	if isRemoteSrc {
		xcon.SetSourceConnection(&remote_connection.Connection{Id: srcID})
	} else {
		xcon.SetSourceConnection(&local_connection.Connection{Id: srcID})
	}

	if isRemoteDst {
		xcon.SetDestinationConnection(&remote_connection.Connection{Id: dstID})
	} else {
		xcon.SetDestinationConnection(&local_connection.Connection{Id: dstID})
	}

	return xcon
}

func (data *healTestData) createRequest(isRemote bool) networkservice.Request {
	if isRemote {
		return &remote_networkservice.NetworkServiceRequest{
			Connection: &remote_connection.Connection{
				NetworkService: networkServiceName,
			},
		}
	}

	return &local_networkservice.NetworkServiceRequest{
		Connection: &local_connection.Connection{
			NetworkService: networkServiceName,
		},
	}

}

func (data *healTestData) createClientConnection(id string, xcon *crossconnect.CrossConnect, nse *registry.NSERegistration, nsm, dataplane string, request networkservice.Request) *model.ClientConnection {
	return &model.ClientConnection{
		ConnectionID: id,
		Xcon:         xcon,
		RemoteNsm: &registry.NetworkServiceManager{
			Name: nsm,
		},
		Endpoint:                nse,
		DataplaneRegisteredName: dataplane,
		Request:                 request,
		DataplaneState:          model.DataplaneStateReady,
	}
}

func (data *healTestData) cloneClientConnection(connection *model.ClientConnection) *model.ClientConnection {
	id := connection.GetID()
	xcon := proto.Clone(connection.Xcon).(*crossconnect.CrossConnect)
	nse := data.createEndpoint(connection.Endpoint.GetNetworkServiceEndpoint().GetName(), connection.Endpoint.GetNetworkServiceManager().GetName())
	nsm := connection.RemoteNsm.GetName()
	dataplane := connection.DataplaneRegisteredName
	request := data.createRequest(connection.Request.IsRemote())

	if request.IsRemote() {
		request.(*remote_networkservice.NetworkServiceRequest).Connection.Id = id
	} else {
		request.(*local_networkservice.NetworkServiceRequest).Connection.Id = id
	}

	return data.createClientConnection(id, xcon, nse, nsm, dataplane, request)
}

func (data *healTestData) createFindNetworkServiceResponse(nses ...*registry.NSERegistration) *registry.FindNetworkServiceResponse {
	response := &registry.FindNetworkServiceResponse{
		NetworkService: &registry.NetworkService{
			Name: networkServiceName,
		},
		NetworkServiceManagers:  map[string]*registry.NetworkServiceManager{},
		NetworkServiceEndpoints: []*registry.NetworkServiceEndpoint{},
	}

	for _, nse := range nses {
		nsm := nse.GetNetworkServiceManager().GetName()
		response.NetworkServiceManagers[nsm] = &registry.NetworkServiceManager{
			Name: nsm,
		}
		response.NetworkServiceEndpoints = append(response.NetworkServiceEndpoints, nse.GetNetworkServiceEndpoint())
	}

	return response
}
