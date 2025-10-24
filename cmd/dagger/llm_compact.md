# Conversation Summary Prompt

Your task is to create a comprehensive summary of the conversation so far. This summary will be used to continue the conversation with reduced token usage while preserving all essential context.

Before providing your final summary, wrap your analysis in `<analysis>` tags to organize your thoughts and ensure complete coverage.

## Analysis Process

In your analysis, chronologically review the conversation and identify:

1. **User Requests**: All explicit requests, questions, and intents expressed by the user
2. **Your Actions**: How you approached and addressed each request
3. **Technical Details**: Key decisions, code patterns, architectural choices, and implementation details
4. **Specifics**: File names, code snippets, function signatures, commands run, and edits made
5. **Issues**: Errors encountered and their resolutions
6. **User Feedback**: Corrections, clarifications, or direction changes from the user

## Summary Structure

Your summary must include these sections:

### 1. Primary Request and Intent
Capture all user requests and intents in detail, including the broader goals and specific asks.

### 2. Key Technical Concepts
List important technical concepts, technologies, frameworks, patterns, and architectural decisions discussed.

### 3. Files and Code Sections
Document specific files examined, modified, or created:
- File path
- Purpose and context
- Summary of changes made
- Relevant code snippets (especially from recent messages)

### 4. Errors and Fixes
List errors encountered and their resolutions:
- Error description and context
- How the error was resolved
- Any user feedback related to the error

### 5. Problem Solving
Document problems solved, approaches tried, and any ongoing troubleshooting efforts.

### 6. User Messages
List ALL non-tool-result user messages. These capture user feedback, changing intent, and explicit directions.

### 7. Pending Tasks
List any tasks explicitly requested but not yet completed or started.

### 8. Current Work
Describe precisely what was being worked on immediately before this summary. Include:
- The specific task or problem
- Files being edited
- Code snippets being worked on
- Current state (in progress, blocked, testing, etc.)

### 9. Next Step (Optional)
If there is a clear next step based on the most recent work:
- List the specific next action to take
- Include direct quotes from the conversation showing what task you were working on
- Ensure alignment with explicit user requests
- If the last task was concluded, only list next steps if explicitly requested by the user

**Important**: Do not infer tangential next steps. Stay focused on explicit requests.

## Output Format

```
<analysis>
[Your chronological analysis ensuring all points are covered thoroughly]
</analysis>

<summary>
1. Primary Request and Intent:
   [Detailed description]

2. Key Technical Concepts:
   - [Concept 1]
   - [Concept 2]
   - ...

3. Files and Code Sections:
   - [File path 1]
     - Purpose: [Why this file is important]
     - Changes: [Summary of modifications]
     - Code: [Relevant snippets]
   - [File path 2]
     - ...

4. Errors and Fixes:
   - [Error 1 description]
     - Resolution: [How it was fixed]
     - Feedback: [User feedback if any]
   - ...

5. Problem Solving:
   [Description of solved problems and ongoing troubleshooting]

6. User Messages:
   - [User message 1]
   - [User message 2]
   - ...

7. Pending Tasks:
   - [Task 1]
   - [Task 2]
   - ...

8. Current Work:
   [Precise description of what was being worked on before this summary]

9. Next Step:
   [Optional next action if clearly defined and aligned with user requests]
   
   Context from conversation:
   > "[Direct quote showing the task context]"
</summary>
```

## Additional Instructions

Custom summarization instructions may be provided in the context. Follow those instructions when creating this summary. Examples:

- "Focus on TypeScript code changes and mistakes made"
- "Include test output and code changes; include file reads verbatim"
- "Emphasize database schema changes and migration steps"

Apply any such instructions while maintaining the structure above.
