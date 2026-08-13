package toolmw

import (
	"context"
	"io"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// InvokableToStreamable 将 Invokable 中间件转换为 Streamable 中间件
// Streamable 实现复用 Invokable 的函数，返回结果是假流式（单元素流）
func InvokableToStreamable(invokable compose.InvokableToolMiddleware) compose.StreamableToolMiddleware {
	return func(next compose.StreamableToolEndpoint) compose.StreamableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.StreamToolOutput, error) {
			// 创建一个假的 InvokableToolEndpoint，它会消费流并返回完整结果
			fakeInvokableNext := func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				// 调用原始的流式 endpoint
				streamOutput, err := next(ctx, input)
				if err != nil {
					return nil, err
				}

				// 消费流，合并所有结果
				result, err := concatStreamReader(streamOutput.Result)
				if err != nil {
					return nil, err
				}

				return &compose.ToolOutput{Result: result}, nil
			}

			// 应用 Invokable 中间件
			wrappedInvokable := invokable(fakeInvokableNext)

			// 执行并获取结果
			output, err := wrappedInvokable(ctx, input)
			if err != nil {
				return nil, err
			}

			// 将结果转换为假流式（单元素流）
			return &compose.StreamToolOutput{
				Result: schema.StreamReaderFromArray([]string{output.Result}),
			}, nil
		}
	}
}

// concatStreamReader 消费 StreamReader 并将所有结果拼接成一个字符串
func concatStreamReader(reader *schema.StreamReader[string]) (string, error) {
	var result string
	defer reader.Close()

	for {
		chunk, err := reader.Recv()
		if err != nil {
			// io.EOF 表示流结束
			if err == io.EOF {
				break
			}
			return "", err
		}
		result += chunk
	}

	return result, nil
}
