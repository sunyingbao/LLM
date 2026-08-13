package summarization

import "eino-cli/deepagent/core/constant"

const defaultSummaryPromptEN = `You are a conversation summarizer for an AI coding agent. Your task is to compress conversation history while preserving all information needed to continue the task.

<conversation_history>
%s
</conversation_history>

Generate a structured summary in the following format:

<summary_format>
## User Intent
- The user's original request and high-level goal
- Any clarifications or scope changes from the conversation

## Completed Work
- Important actions in chronological order
- File changes: record paths and specific changes
- Tool calls: record tool names and key results
- Commands: record commands and whether they succeeded or failed

## Current State
- The current or most recently completed task
- Any remaining work
- Files already read or modified
- Working directory or relevant environment context

## Key Decisions and Context
- Important decisions and reasons
- Constraints or requirements discovered during execution
- Errors encountered and how they were handled

## Active Artifacts
- Code snippets, configuration, or data still relevant to the current task
- Variable values or state needed to continue
</summary_format>

Rules:
1. Preserve file paths, function names, variable names, and error messages exactly
2. Preserve code snippets only when they are still being processed or referenced
3. Omit greetings, confirmations, and redundant back-and-forth
4. If a tool result is not meaningful for the task, summarize it in one line
5. Use specific descriptions rather than vague statements
6. The summary must be self-contained

Generate the summary now.`

const chainSummaryPromptEN = `You are a conversation summarizer for an AI coding agent. Merge the existing summary and the new conversation history into one complete, coherent summary.

<previous_summary>
%s
</previous_summary>

<new_conversation>
%s
</new_conversation>

Generate the merged summary in the following format:

<summary_format>
## User Intent
- The user's original request and high-level goal inherited from the previous summary
- Any clarifications or scope changes from the new conversation

## Completed Work
- Merge old and new actions in chronological order
- File changes: record paths and specific changes
- Tool calls: record tool names and key results
- Merge repeated operations into one concise entry

## Current State
- The current task based on the latest conversation
- Any remaining work
- Files already read or modified
- Working directory or relevant environment context

## Key Decisions and Context
- Important decisions from both the previous summary and new conversation
- Remove decisions that were later reverted or superseded
- Constraints or requirements discovered during execution

## Active Artifacts
- Code snippets, configuration, or data still relevant to the current task
- Discard outdated artifacts from the old summary
- Add new artifacts from the latest conversation
</summary_format>

Rules:
1. Preserve file paths, function names, variable names, and error messages exactly
2. If information conflicts, prefer the new conversation
3. Remove operations that were later reverted or superseded
4. Keep only artifacts needed to continue the current task
5. The merged summary must not be longer than the previous summary plus the new conversation
6. The summary must be self-contained

Generate the merged summary now.`

func defaultSummaryPrompt() string {
	if constant.IsEnglishPromptLang() {
		return defaultSummaryPromptEN
	}
	return constant.DefaultSummaryPrompt
}

func chainSummaryPrompt() string {
	if constant.IsEnglishPromptLang() {
		return chainSummaryPromptEN
	}
	return constant.ChainSummaryPrompt
}
