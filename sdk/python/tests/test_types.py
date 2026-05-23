from agentserver_sdk.types import ShellResult, ToolMetadata


def test_shell_result_from_mcp_text_content():
    raw = {
        "content": [{"type": "text", "text": "hi"}],
        "structuredContent": {"stdout": "hi", "stderr": "", "exit_code": 0},
        "isError": False,
    }
    r = ShellResult.from_mcp(raw)
    assert r.stdout == "hi"
    assert r.stderr == ""
    assert r.exit_code == 0


def test_shell_result_from_mcp_fallback_when_no_structured():
    raw = {"content": [{"type": "text", "text": "fallback"}], "isError": False}
    r = ShellResult.from_mcp(raw)
    assert r.stdout == "fallback"
    assert r.stderr == ""
    assert r.exit_code == 0


def test_shell_result_exit_code_nonzero():
    raw = {
        "content": [{"type": "text", "text": ""}],
        "structuredContent": {"stdout": "", "stderr": "boom", "exit_code": 1},
        "isError": False,
    }
    r = ShellResult.from_mcp(raw)
    assert r.exit_code == 1
    assert r.stderr == "boom"


def test_tool_metadata_from_dict():
    m = ToolMetadata.from_dict(
        {
            "name": "submit_task",
            "description": "submit HPC job",
            "inputSchema": {"type": "object"},
        }
    )
    assert m.name == "submit_task"
    assert m.description == "submit HPC job"
    assert m.kind == "custom"  # default for non-core


def test_tool_metadata_core_marker():
    m = ToolMetadata.from_dict({"name": "shell", "description": "x", "inputSchema": {}})
    assert m.kind == "core"
