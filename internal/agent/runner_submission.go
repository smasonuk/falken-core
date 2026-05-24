package agent

import (
	"encoding/json"
	"errors"
)

var (
	// ErrImplementationSubmissionRequired indicates the model tried to finish twice without submitting active implementation state.
	ErrImplementationSubmissionRequired = errors.New("implementation submission required before final response")
)

func (r *Runner) requiresImplementationSubmission() (bool, error) {
	if r == nil || r.history == nil {
		return false, nil
	}
	plan, err := r.history.ReadPlan()
	if err != nil {
		return false, err
	}
	if isActivePlanText(plan) {
		return true, nil
	}
	todos, err := r.history.ReadTodos()
	if err != nil {
		return false, err
	}
	return len(todos) != 0, nil
}

func renderSubmitPlanImplementationRequired() string {
	return "You attempted to finish, but this run has runtime todos from an implementation plan.\n" +
		"Before the final response, call submit_plan_implementation after completing the plan,\n" +
		"updating todos, and summarizing verification."
}

func submissionAccepted(result ToolResult) bool {
	if len(result.Payload) == 0 {
		return false
	}
	var payload struct {
		Success  bool `json:"success"`
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		return false
	}
	return payload.Success && payload.Accepted
}

func todoOrPlanMutationAccepted(result ToolResult) bool {
	switch result.Name {
	case "write_plan", "write_todos":
	default:
		return false
	}
	return toolResultSucceeded(result)
}

func readyForImplementation(result ToolResult) bool {
	if len(result.Payload) == 0 {
		return false
	}
	var payload struct {
		Ready        bool `json:"ready_for_implementation"`
		PlanValid    bool `json:"plan_valid"`
		TodosPresent bool `json:"todos_written"`
		TodosValid   bool `json:"todos_valid"`
	}
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		return false
	}
	return payload.Ready && payload.PlanValid && payload.TodosPresent && payload.TodosValid
}
