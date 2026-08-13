package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	cbutils "github.com/cloudwego/eino/utils/callbacks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTool 是一个用于测试的模拟工具
type mockTool struct {
	name      string
	invokeErr error
	response  string
}

func (m *mockTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: m.name,
		Desc: "mock tool for testing",
	}, nil
}

func (m *mockTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	if m.invokeErr != nil {
		return "", m.invokeErr
	}
	if m.response != "" {
		return m.response, nil
	}
	return "mock response for: " + argumentsInJSON, nil
}

type mockInvokableStreamableTool struct {
	*mockTool
}

func (m *mockInvokableStreamableTool) StreamableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return schema.StreamReaderFromArray([]string{"stream response for: " + argumentsInJSON}), nil
}

type mockInvokableEnhancedStreamableTool struct {
	*mockTool
}

func (m *mockInvokableEnhancedStreamableTool) StreamableRun(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	return schema.StreamReaderFromArray([]*schema.ToolResult{textToolResult("enhanced stream response for: " + toolArgument.Text)}), nil
}

type mockEnhancedInvokableStreamableTool struct {
	name string
}

func (m *mockEnhancedInvokableStreamableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: m.name, Desc: "mock enhanced invokable streamable tool"}, nil
}

func (m *mockEnhancedInvokableStreamableTool) InvokableRun(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
	return textToolResult("enhanced invoke response for: " + toolArgument.Text), nil
}

func (m *mockEnhancedInvokableStreamableTool) StreamableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return schema.StreamReaderFromArray([]string{"stream response for: " + argumentsInJSON}), nil
}

type mockEnhancedInvokableEnhancedStreamableTool struct {
	name string
}

func (m *mockEnhancedInvokableEnhancedStreamableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: m.name, Desc: "mock enhanced invokable enhanced streamable tool"}, nil
}

func (m *mockEnhancedInvokableEnhancedStreamableTool) InvokableRun(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
	return textToolResult("enhanced invoke response for: " + toolArgument.Text), nil
}

func (m *mockEnhancedInvokableEnhancedStreamableTool) StreamableRun(ctx context.Context, toolArgument *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	return schema.StreamReaderFromArray([]*schema.ToolResult{textToolResult("enhanced stream response for: " + toolArgument.Text)}), nil
}

func TestNewWrapTool(t *testing.T) {
	mock := &mockTool{name: "test-tool"}
	wrapped := NewWrapTool(mock, nil, nil)

	assert.NotNil(t, wrapped)
	assert.Equal(t, mock, wrapped.baseTool)
}

func TestWrapTool_Info(t *testing.T) {
	ctx := context.Background()
	mock := &mockTool{name: "test-tool"}
	wrapped := NewWrapTool(mock, nil, nil)

	info, err := wrapped.Info(ctx)
	require.NoError(t, err)
	assert.Equal(t, "test-tool", info.Name)
}

func TestWrapTool_InvokableRun(t *testing.T) {
	ctx := context.Background()

	t.Run("without processors", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}
		wrapped := NewWrapTool(mock, nil, nil)

		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		require.NoError(t, err)
		assert.Contains(t, result, "mock response")
	})

	t.Run("with preprocess", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}
		preprocess := func(ctx context.Context, baseTool tool.InvokableTool, args string) (string, error) {
			return args + " [preprocessed]", nil
		}
		wrapped := NewWrapTool(mock, []ToolRequestPreprocess{preprocess}, nil)

		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		require.NoError(t, err)
		assert.Contains(t, result, "[preprocessed]")
	})

	t.Run("with postprocess", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}
		postprocess := func(ctx context.Context, baseTool tool.InvokableTool, response, args string) (string, error) {
			return response + " [postprocessed]", nil
		}
		wrapped := NewWrapTool(mock, nil, []ToolResponsePostprocess{postprocess})

		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		require.NoError(t, err)
		assert.Contains(t, result, "[postprocessed]")
	})

	t.Run("with multiple processors", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}

		preprocess1 := func(ctx context.Context, baseTool tool.InvokableTool, args string) (string, error) {
			return args + " [pre1]", nil
		}
		preprocess2 := func(ctx context.Context, baseTool tool.InvokableTool, args string) (string, error) {
			return args + " [pre2]", nil
		}
		postprocess1 := func(ctx context.Context, baseTool tool.InvokableTool, response, args string) (string, error) {
			return response + " [post1]", nil
		}
		postprocess2 := func(ctx context.Context, baseTool tool.InvokableTool, response, args string) (string, error) {
			return response + " [post2]", nil
		}

		wrapped := NewWrapTool(mock,
			[]ToolRequestPreprocess{preprocess1, preprocess2},
			[]ToolResponsePostprocess{postprocess1, postprocess2},
		)

		result, err := wrapped.InvokableRun(ctx, `input`)
		require.NoError(t, err)
		// 预处理应该按顺序执行
		assert.Contains(t, result, "[pre1]")
		assert.Contains(t, result, "[pre2]")
		// 后处理应该按顺序执行
		assert.Contains(t, result, "[post1]")
		assert.Contains(t, result, "[post2]")
	})

	t.Run("preprocess error", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}
		preprocess := func(ctx context.Context, baseTool tool.InvokableTool, args string) (string, error) {
			return "", errors.New("preprocess error")
		}
		wrapped := NewWrapTool(mock, []ToolRequestPreprocess{preprocess}, nil)

		// 错误应该被转换为字符串返回，而不是返回 error
		// 这样可以避免图停止运行
		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		assert.NoError(t, err)
		assert.Contains(t, result, "[Error]")
		assert.Contains(t, result, "preprocess error")
	})

	t.Run("postprocess error", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}
		postprocess := func(ctx context.Context, baseTool tool.InvokableTool, response, args string) (string, error) {
			return "", errors.New("postprocess error")
		}
		wrapped := NewWrapTool(mock, nil, []ToolResponsePostprocess{postprocess})

		// 错误应该被转换为字符串返回，而不是返回 error
		// 这样可以避免图停止运行
		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		assert.NoError(t, err)
		assert.Contains(t, result, "[Error]")
		assert.Contains(t, result, "postprocess error")
	})

	t.Run("emits callbacks", func(t *testing.T) {
		mock := &mockTool{name: "test-tool"}
		wrapped := NewWrapTool(mock, nil, nil)
		var starts, ends int
		handler := cbutils.NewHandlerHelper().Tool(&cbutils.ToolCallbackHandler{
			OnStart: func(ctx context.Context, info *callbacks.RunInfo, input *tool.CallbackInput) context.Context {
				starts++
				assert.Equal(t, `{"key":"value"}`, input.ArgumentsInJSON)
				return ctx
			},
			OnEnd: func(ctx context.Context, info *callbacks.RunInfo, output *tool.CallbackOutput) context.Context {
				ends++
				assert.Contains(t, output.Response, "mock response")
				return ctx
			},
		}).Handler()
		cbCtx := callbacks.InitCallbacks(context.Background(), &callbacks.RunInfo{
			Name:      "test-tool",
			Component: components.ComponentOfTool,
		}, handler)

		result, err := wrapped.InvokableRun(cbCtx, `{"key":"value"}`)
		require.NoError(t, err)
		assert.Contains(t, result, "mock response")
		assert.Equal(t, 1, starts)
		assert.Equal(t, 1, ends)

		_, err = wrapped.InvokableRun(WithWrapperCallbacksDisabled(cbCtx), `{"key":"value"}`)
		require.NoError(t, err)
		assert.Equal(t, 1, starts)
		assert.Equal(t, 1, ends)
	})

	t.Run("local func argument error becomes tool result", func(t *testing.T) {
		localFuncErr := errors.New(`[LocalFunc] failed to unmarshal arguments in json, toolName=update_plan, err=Mismatch type []middleware.PlanStep with value string`)
		mock := &mockTool{name: "test-tool", invokeErr: localFuncErr}
		wrapped := NewWrapTool(mock, nil, nil)

		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		assert.NoError(t, err)
		assert.Contains(t, result, "[Error]")
		assert.Contains(t, result, "Tool invocation failed")
		assert.Contains(t, result, "failed to unmarshal arguments")
	})

	t.Run("base tool error is propagated", func(t *testing.T) {
		mock := &mockTool{name: "test-tool", invokeErr: errors.New("invoke error")}
		wrapped := NewWrapTool(mock, nil, nil)

		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		assert.Error(t, err)
		assert.Empty(t, result)
		assert.Contains(t, err.Error(), "invoke error")
	})

	t.Run("interrupt error is propagated", func(t *testing.T) {
		interruptErr := tool.StatefulInterrupt(ctx, "needs approval", "state")
		require.Error(t, interruptErr)

		mock := &mockTool{name: "test-tool", invokeErr: interruptErr}
		wrapped := NewWrapTool(mock, nil, nil)

		result, err := wrapped.InvokableRun(ctx, `{"key": "value"}`)
		assert.Error(t, err)
		assert.Empty(t, result)
		assert.Contains(t, err.Error(), "interrupt signal:")
	})
}

func TestWrapTool_InfoWithRewriter(t *testing.T) {
	ctx := context.Background()

	t.Run("rewriter modifies name and desc", func(t *testing.T) {
		mock := &mockTool{name: "original_tool"}
		rewriter := func(ctx context.Context, info *schema.ToolInfo) *schema.ToolInfo {
			info.Name = "renamed_tool"
			info.Desc = "new description"
			return info
		}
		wrapped := &WrapTool{
			baseTool:     mock,
			infoRewriter: rewriter,
		}

		info, err := wrapped.Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "renamed_tool", info.Name)
		assert.Equal(t, "new description", info.Desc)
	})

	t.Run("nil rewriter preserves original", func(t *testing.T) {
		mock := &mockTool{name: "original_tool"}
		wrapped := &WrapTool{
			baseTool:     mock,
			infoRewriter: nil,
		}

		info, err := wrapped.Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "original_tool", info.Name)
		assert.Equal(t, "mock tool for testing", info.Desc)
	})

	t.Run("rewriter only modifies name", func(t *testing.T) {
		mock := &mockTool{name: "original_tool"}
		rewriter := func(ctx context.Context, info *schema.ToolInfo) *schema.ToolInfo {
			info.Name = "renamed"
			return info
		}
		wrapped := &WrapTool{
			baseTool:     mock,
			infoRewriter: rewriter,
		}

		info, err := wrapped.Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "renamed", info.Name)
		assert.Equal(t, "mock tool for testing", info.Desc)
	})
}

func TestWrapToolsWithConfig(t *testing.T) {
	t.Run("with info rewriter", func(t *testing.T) {
		mock1 := &mockTool{name: "tool1"}
		mock2 := &mockTool{name: "tool2"}
		rewriter := func(ctx context.Context, info *schema.ToolInfo) *schema.ToolInfo {
			if info.Name == "tool1" {
				info.Name = "renamed_tool1"
				info.Desc = "new desc"
			}
			return info
		}

		toolList := []tool.BaseTool{mock1, mock2}
		wrapped := WrapToolsWithConfig(toolList, &WrapToolsConfig{
			InfoRewriter: rewriter,
		})

		ctx := context.Background()
		info1, err := wrapped[0].Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "renamed_tool1", info1.Name)
		assert.Equal(t, "new desc", info1.Desc)

		info2, err := wrapped[1].Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "tool2", info2.Name)
	})

	t.Run("nil rewriter in config", func(t *testing.T) {
		mock := &mockTool{name: "tool1"}
		wrapped := WrapToolsWithConfig([]tool.BaseTool{mock}, &WrapToolsConfig{
			Preprocess: []ToolRequestPreprocess{ToolRequestRepairJSON},
		})

		ctx := context.Background()
		info, err := wrapped[0].Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "tool1", info.Name)
	})

	t.Run("deprecated WrapTools still works", func(t *testing.T) {
		mock := &mockTool{name: "tool1"}
		wrapped := WrapTools([]tool.BaseTool{mock}, nil, nil)

		ctx := context.Background()
		info, err := wrapped[0].Info(ctx)
		require.NoError(t, err)
		assert.Equal(t, "tool1", info.Name)
	})

	t.Run("preserves multi-interface capabilities", func(t *testing.T) {
		tests := []struct {
			name     string
			baseTool tool.BaseTool
			assertFn func(t *testing.T, wrapped tool.BaseTool)
		}{
			{
				name:     "invokable streamable",
				baseTool: &mockInvokableStreamableTool{mockTool: &mockTool{name: "invokable_streamable"}},
				assertFn: func(t *testing.T, wrapped tool.BaseTool) {
					_, ok := wrapped.(tool.InvokableTool)
					assert.True(t, ok)
					_, ok = wrapped.(tool.StreamableTool)
					assert.True(t, ok)
				},
			},
			{
				name:     "invokable enhanced streamable",
				baseTool: &mockInvokableEnhancedStreamableTool{mockTool: &mockTool{name: "invokable_enhanced_streamable"}},
				assertFn: func(t *testing.T, wrapped tool.BaseTool) {
					_, ok := wrapped.(tool.InvokableTool)
					assert.True(t, ok)
					_, ok = wrapped.(tool.EnhancedStreamableTool)
					assert.True(t, ok)
				},
			},
			{
				name:     "enhanced invokable streamable",
				baseTool: &mockEnhancedInvokableStreamableTool{name: "enhanced_invokable_streamable"},
				assertFn: func(t *testing.T, wrapped tool.BaseTool) {
					_, ok := wrapped.(tool.EnhancedInvokableTool)
					assert.True(t, ok)
					_, ok = wrapped.(tool.StreamableTool)
					assert.True(t, ok)
				},
			},
			{
				name:     "enhanced invokable enhanced streamable",
				baseTool: &mockEnhancedInvokableEnhancedStreamableTool{name: "enhanced_invokable_enhanced_streamable"},
				assertFn: func(t *testing.T, wrapped tool.BaseTool) {
					_, ok := wrapped.(tool.EnhancedInvokableTool)
					assert.True(t, ok)
					_, ok = wrapped.(tool.EnhancedStreamableTool)
					assert.True(t, ok)
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				wrapped := WrapToolsWithConfig([]tool.BaseTool{tt.baseTool}, &WrapToolsConfig{})[0]
				tt.assertFn(t, wrapped)
				callbacksEnabled, ok := wrapped.(interface{ IsCallbacksEnabled() bool })
				require.True(t, ok)
				assert.True(t, callbacksEnabled.IsCallbacksEnabled())
			})
		}
	})
}

func TestToolRequestRepairJSON(t *testing.T) {
	ctx := context.Background()
	mock := &mockTool{name: "test-tool"}

	t.Run("repairs malformed JSON", func(t *testing.T) {
		result, err := ToolRequestRepairJSON(ctx, mock, `<|FunctionCallBegin|>{"key": "value"}`)
		require.NoError(t, err)
		assert.Equal(t, `{"key": "value"}`, result)
	})

	t.Run("valid JSON unchanged", func(t *testing.T) {
		result, err := ToolRequestRepairJSON(ctx, mock, `{"key": "value"}`)
		require.NoError(t, err)
		assert.Equal(t, `{"key": "value"}`, result)
	})
}

func TestWrapTools(t *testing.T) {
	t.Run("wrap multiple tools", func(t *testing.T) {
		mock1 := &mockTool{name: "tool1"}
		mock2 := &mockTool{name: "tool2"}

		tools := []tool.BaseTool{mock1, mock2}
		wrapped := WrapTools(tools, nil, nil)

		assert.Len(t, wrapped, 2)
		// 验证是 WrapTool 类型
		_, ok1 := wrapped[0].(*WrapTool)
		assert.True(t, ok1)
		_, ok2 := wrapped[1].(*WrapTool)
		assert.True(t, ok2)
	})

	t.Run("empty tools", func(t *testing.T) {
		wrapped := WrapTools([]tool.BaseTool{}, nil, nil)
		assert.Empty(t, wrapped)
	})

	t.Run("with processors", func(t *testing.T) {
		mock := &mockTool{name: "tool1"}
		preprocess := func(ctx context.Context, baseTool tool.InvokableTool, args string) (string, error) {
			return args + " [wrapped]", nil
		}

		tools := []tool.BaseTool{mock}
		wrapped := WrapTools(tools, []ToolRequestPreprocess{preprocess}, nil)

		assert.Len(t, wrapped, 1)

		// 执行包装后的工具
		wt := wrapped[0].(*WrapTool)
		result, err := wt.InvokableRun(context.Background(), "test")
		require.NoError(t, err)
		assert.Contains(t, result, "[wrapped]")
	})
}
