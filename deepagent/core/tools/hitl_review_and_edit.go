package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ReviewEditInfo is presented to the user for editing.
type ReviewEditInfo struct {
	ToolName        string
	ArgumentsInJSON string
	ReviewResult    *ReviewEditResult
}

// ReviewEditResult is the result of the user's review.
type ReviewEditResult struct {
	EditedArgumentsInJSON *string
	NoNeedToEdit          bool
	Disapproved           bool
	DisapproveReason      *string
}

func (re *ReviewEditInfo) String() string {
	return fmt.Sprintf("Tool '%s' is about to be called with the following arguments:\n`\n%s\n`\n\n"+
		"Please review and either provide edited arguments in JSON format, "+
		"reply with 'no need to edit', or reply with 'N' to disapprove the tool call.",
		re.ToolName, re.ArgumentsInJSON)
}

func init() {
	schema.Register[*ReviewEditInfo]()
}

type NeedReviewAndEdit func(ctx context.Context, info *ReviewEditInfo) bool

// InvokableReviewEditTool is a wrapper that enforces a review-and-edit step.
type InvokableReviewEditTool struct {
	tool.InvokableTool
	needReviewAndEdit NeedReviewAndEdit
}

func NewInvokableReviewEditTool(tool tool.InvokableTool, needReviewAndEdit NeedReviewAndEdit) InvokableReviewEditTool {
	return InvokableReviewEditTool{InvokableTool: tool, needReviewAndEdit: needReviewAndEdit}
}

func (i InvokableReviewEditTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return i.InvokableTool.Info(ctx)
}

func (i InvokableReviewEditTool) InvokableRun(ctx context.Context, argumentsInJSON string,
	opts ...tool.Option) (string, error) {

	toolInfo, err := i.Info(ctx)
	if err != nil {
		return "", err
	}

	wasInterrupted, _, storedArguments := tool.GetInterruptState[string](ctx)
	if !wasInterrupted {
		// 如果 lib 使用者认为不需要审批，直接运行
		if i.needReviewAndEdit != nil && i.needReviewAndEdit(ctx, &ReviewEditInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: argumentsInJSON,
		}) {
			return "", tool.StatefulInterrupt(ctx, &ReviewEditInfo{
				ToolName:        toolInfo.Name,
				ArgumentsInJSON: argumentsInJSON,
			}, argumentsInJSON)
		}

		return "", tool.StatefulInterrupt(ctx, &ReviewEditInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: argumentsInJSON,
		}, argumentsInJSON)
	}

	isResumeTarget, hasData, data := tool.GetResumeContext[*ReviewEditInfo](ctx)
	if !isResumeTarget {
		return "", tool.StatefulInterrupt(ctx, &ReviewEditInfo{
			ToolName:        toolInfo.Name,
			ArgumentsInJSON: storedArguments,
		}, storedArguments)
	}
	if !hasData || data.ReviewResult == nil {
		return "", fmt.Errorf("tool '%s' resumed with no review data", toolInfo.Name)
	}

	result := data.ReviewResult

	if result.Disapproved {
		return formatHumanRejectedToolResult(toolInfo.Name, result.DisapproveReason), nil
	}

	if result.NoNeedToEdit {
		return i.InvokableTool.InvokableRun(ctx, storedArguments, opts...)
	}

	if result.EditedArgumentsInJSON != nil {
		res, err := i.InvokableTool.InvokableRun(ctx, *result.EditedArgumentsInJSON, opts...)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("after presenting the tool call info to the user, the user explilcitly changed tool call arguments to %s. Tool called, final result: %s",
			*result.EditedArgumentsInJSON, res), nil
	}

	return "", fmt.Errorf("invalid review result for tool '%s'", toolInfo.Name)
}
