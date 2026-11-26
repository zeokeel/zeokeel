package gateway

// presets.* (M3): the role roster over the wire — list, create/override, delete,
// and the two refusals whose wording matters (invalid role, built-in delete).

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakePresets struct {
	rows    []contracts.AgentPreset
	put     *contracts.AgentPreset
	deleted string
	putErr  error
	delErr  error
}

func (f *fakePresets) List(context.Context) ([]contracts.AgentPreset, error) { return f.rows, nil }
func (f *fakePresets) Get(_ context.Context, id string) (contracts.AgentPreset, bool, error) {
	for _, row := range f.rows {
		if row.ID == id {
			return row, true, nil
		}
	}
	return contracts.AgentPreset{}, false, nil
}
func (f *fakePresets) Put(_ context.Context, preset contracts.AgentPreset) (contracts.AgentPreset, error) {
	if f.putErr != nil {
		return contracts.AgentPreset{}, f.putErr
	}
	f.put = &preset
	return preset, nil
}
func (f *fakePresets) Delete(_ context.Context, id string) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = id
	return nil
}

func presetGateway(t *testing.T, service contracts.AgentPresetService) *Gateway {
	t.Helper()
	gateway := newTestGateway(t, &fakeChat{})
	if service != nil {
		if _, err := gateway.root.Provide(contracts.KeyAgentPresets, service); err != nil {
			t.Fatalf("provide presets: %v", err)
		}
	}
	return gateway
}

func TestPresetSnapshotMarksBuiltins(t *testing.T) {
	service := &fakePresets{rows: []contracts.AgentPreset{
		{ID: contracts.PresetLeadID, Name: "lead"},
		{ID: "reviewer", Name: "复核员", Deny: []string{"bash"}, MCPServers: []string{"files"}, Skills: []string{"deploy"}},
	}}
	gateway := presetGateway(t, service)
	answer, problem := gateway.presetSnapshot(context.Background(), nil)
	if problem != nil {
		t.Fatalf("presetSnapshot: %+v", problem)
	}
	result, ok := answer.(presetSnapshotResult)
	if !ok || len(result.Presets) != 2 {
		t.Fatalf("answer = %T %+v", answer, result)
	}
	byID := map[string]presetDTO{}
	for _, dto := range result.Presets {
		byID[dto.ID] = dto
	}
	if !byID[contracts.PresetLeadID].Builtin {
		t.Fatal("lead should be marked builtin")
	}
	if byID["reviewer"].Builtin || len(byID["reviewer"].Deny) != 1 {
		t.Fatalf("reviewer = %+v, want a non-builtin role carrying its deny list", byID["reviewer"])
	}
	if len(byID["reviewer"].MCPServers) != 1 || len(byID["reviewer"].Skills) != 1 {
		t.Fatalf("reviewer = %+v, want its MCP and skill scope carried over the wire", byID["reviewer"])
	}
}

func TestPresetPutMapsTheRole(t *testing.T) {
	service := &fakePresets{}
	gateway := presetGateway(t, service)
	body, err := json.Marshal(presetParams{ID: "reviewer", Name: "复核员", Tools: []string{"read_file"}, Deny: []string{"bash"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, problem := gateway.presetPut(context.Background(), body); problem != nil {
		t.Fatalf("presetPut: %+v", problem)
	}
	if service.put == nil || service.put.ID != "reviewer" || len(service.put.Deny) != 1 {
		t.Fatalf("put = %+v, want the mapped role", service.put)
	}
}

func TestPresetPutReportsInvalidAsParams(t *testing.T) {
	service := &fakePresets{putErr: contracts.ErrAgentPresetInvalid}
	gateway := presetGateway(t, service)
	body, _ := json.Marshal(presetParams{ID: "x", Name: "y"})
	_, problem := gateway.presetPut(context.Background(), body)
	if problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("problem = %+v, want codeInvalidParams", problem)
	}
}

func TestPresetDeleteRefusesABuiltinWithAReadableMessage(t *testing.T) {
	service := &fakePresets{delErr: contracts.ErrPresetBuiltin}
	gateway := presetGateway(t, service)
	body, _ := json.Marshal(presetIDParams{ID: contracts.PresetAuditorID})
	_, problem := gateway.presetDelete(context.Background(), body)
	if problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("problem = %+v, want a request-level refusal", problem)
	}
	if problem.Message == "" || problem.Message == "invalid params" {
		t.Fatalf("message = %q, want the reason", problem.Message)
	}
}

func TestPresetsUnavailableWithoutTheModule(t *testing.T) {
	gateway := presetGateway(t, nil)
	if _, problem := gateway.presetSnapshot(context.Background(), nil); problem == nil {
		t.Fatal("presetSnapshot answered without a backend")
	}
}
