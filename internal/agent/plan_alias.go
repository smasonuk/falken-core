package agent

import "github.com/smasonuk/falken-core/internal/conversation"

type PlanManager = conversation.PlanManager

var ErrInvalidPlan = conversation.ErrInvalidPlan

const defaultPlanStarterText = conversation.DefaultPlanStarterText

var NewPlanManager = conversation.NewPlanManager
var ValidateImplementationPlan = conversation.ValidateImplementationPlan

func ValidatePlan(plan string) error {
	return conversation.ValidateImplementationPlan(plan)
}
