"""SAME memory plugin for Hermes Agent.

Cross-agent memory with provenance, trust state, and invalidation tracking.
SAME stores knowledge as markdown notes in a local vault with semantic search,
decision logging, and session handoffs.

Requires the ``same`` binary on PATH (or configured via SAME_BINARY env var)
and a vault initialized with ``same init``.

Config via environment variables:
  SAME_VAULT_PATH  -- Path to the SAME vault directory (required)
  SAME_BINARY      -- Path to the same binary (default: "same")
  SAME_AGENT       -- Agent attribution for writes (default: "hermes")

Or via $HERMES_HOME/same/config.json (profile-scoped).
"""

from __future__ import annotations

import json
import logging
import os
import re
import select
import shutil
import subprocess
import threading
import time
from pathlib import Path
from typing import Any, Dict, List

from agent.memory_provider import MemoryProvider

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# MCP stdio client — talks to `same mcp` subprocess via JSON-RPC 2.0
# ---------------------------------------------------------------------------

class MCPStdioClient:
    """Minimal JSON-RPC 2.0 client for the SAME MCP server over stdio."""

    def __init__(self, command: str, args: list[str], env: dict[str, str] | None = None):
        self._command = command
        self._args = args
        # B-06: Minimal env — don't leak API keys/tokens to subprocess
        minimal_env = {
            "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
            "HOME": os.environ.get("HOME", ""),
            "TMPDIR": os.environ.get("TMPDIR", "/tmp"),
            "LANG": os.environ.get("LANG", "en_US.UTF-8"),
        }
        # SAME needs OLLAMA_URL if configured
        if os.environ.get("OLLAMA_URL"):
            minimal_env["OLLAMA_URL"] = os.environ["OLLAMA_URL"]
        self._env = {**minimal_env, **(env or {})}
        self._proc: subprocess.Popen | None = None
        self._lock = threading.Lock()
        self._request_id = 0
        self._initialized = False
        self._stderr_thread: threading.Thread | None = None
        # Version reported by the MCP server in the initialize handshake
        self.server_version: str = ""

    def start(self, timeout: float = 15.0) -> None:
        """Start the MCP subprocess and complete the handshake."""
        self._proc = subprocess.Popen(
            [self._command] + self._args,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=self._env,
        )
        # MCP initialize handshake
        resp = self._rpc("initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "hermes-same-plugin", "version": "1.0.0"},
        }, timeout=timeout)
        if resp is None:
            stderr = self._drain_stderr()
            raise RuntimeError(
                f"SAME MCP server did not respond to initialize. stderr: {stderr}"
            )
        # Extract server version from the handshake response (serverInfo.version)
        result = resp.get("result", {})
        server_info = result.get("serverInfo", {})
        self.server_version = server_info.get("version", "")
        # Send initialized notification (no response expected)
        self._send_notification("notifications/initialized")
        self._initialized = True
        # Drain stderr in background to prevent buffer deadlock
        self._stderr_thread = threading.Thread(
            target=self._log_stderr, daemon=True, name="same-stderr"
        )
        self._stderr_thread.start()

    def call_tool(self, name: str, arguments: dict | None = None, timeout: float = 30.0) -> str:
        """Call an MCP tool and return the text result."""
        if not self._initialized:
            raise RuntimeError("MCP client not initialized")
        resp = self._rpc("tools/call", {
            "name": name,
            "arguments": arguments or {},
        }, timeout=timeout)
        if resp is None:
            return json.dumps({"error": "No response from SAME MCP server"})
        if "error" in resp:
            return json.dumps({"error": resp["error"].get("message", str(resp["error"]))})
        # Extract text from MCP CallToolResult content array
        result = resp.get("result", {})
        content = result.get("content", [])
        is_error = result.get("isError", False)
        texts = [c.get("text", "") for c in content if c.get("type") == "text"]
        text = "\n".join(texts) if texts else json.dumps(result)
        if is_error:
            return json.dumps({"error": text})
        return text

    def is_alive(self) -> bool:
        return self._proc is not None and self._proc.poll() is None

    def close(self) -> None:
        if self._proc and self._proc.poll() is None:
            try:
                self._proc.stdin.close()
            except Exception:
                pass
            try:
                self._proc.terminate()
                self._proc.wait(timeout=5)
            except Exception:
                try:
                    self._proc.kill()
                except Exception:
                    pass
        self._initialized = False

    # -- internal --

    def _drain_stderr(self) -> str:
        """Read whatever is available on stderr (non-blocking, for errors)."""
        if not self._proc or not self._proc.stderr:
            return ""
        try:
            return self._proc.stderr.read(4096).decode("utf-8", errors="replace")
        except Exception:
            return ""

    def _log_stderr(self) -> None:
        """Drain stderr to logger so the pipe buffer never fills."""
        if not self._proc or not self._proc.stderr:
            return
        for line in self._proc.stderr:
            try:
                text = line.decode("utf-8", errors="replace").rstrip()
                if text:
                    logger.debug("same mcp stderr: %s", text)
            except Exception:
                break

    def _rpc(self, method: str, params: dict, timeout: float = 30.0) -> dict | None:
        """Send a JSON-RPC request and wait for the matching response.

        The lock is held for the entire write+read cycle. This serializes
        concurrent callers, which is correct for stdio (single reader/writer).
        The timeout starts AFTER the lock is acquired so queued callers
        get their full timeout for the actual I/O.
        """
        with self._lock:
            self._request_id += 1
            req_id = self._request_id
            request = {
                "jsonrpc": "2.0",
                "id": req_id,
                "method": method,
                "params": params,
            }
            self._write(request)
            return self._read_response(req_id, timeout)

    def _send_notification(self, method: str, params: dict | None = None) -> None:
        """Send a JSON-RPC notification (no id, no response expected)."""
        with self._lock:
            msg = {"jsonrpc": "2.0", "method": method}
            if params:
                msg["params"] = params
            self._write(msg)

    def _write(self, msg: dict) -> None:
        if not self._proc or not self._proc.stdin:
            raise RuntimeError("MCP subprocess not running")
        line = json.dumps(msg) + "\n"
        self._proc.stdin.write(line.encode("utf-8"))
        self._proc.stdin.flush()

    def _read_response(self, req_id: int, timeout: float) -> dict | None:
        """Read lines until we get a response matching req_id."""
        if not self._proc or not self._proc.stdout:
            return None
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                break
            # Use select for timeout on reads
            ready, _, _ = select.select([self._proc.stdout], [], [], min(remaining, 1.0))
            if not ready:
                continue
            line = self._proc.stdout.readline()
            if not line:
                return None  # EOF — process died
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                continue
            # Skip notifications (no "id" field)
            if "id" not in msg:
                continue
            if msg.get("id") == req_id:
                return msg
        return None


# ---------------------------------------------------------------------------
# Tool schemas (OpenAI function-calling format)
# ---------------------------------------------------------------------------

SEARCH_SCHEMA = {
    "name": "same_search",
    "description": (
        "Search the SAME knowledge vault for relevant notes, decisions, "
        "handoffs, and context. Returns ranked results with provenance "
        "and trust state metadata."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "query": {
                "type": "string",
                "description": "Natural language search query.",
            },
            "top_k": {
                "type": "integer",
                "description": "Number of results to return.",
                "default": 10,
            },
        },
        "required": ["query", "top_k"],
    },
}

SAVE_NOTE_SCHEMA = {
    "name": "same_save_note",
    "description": (
        "Save a markdown note to the SAME vault. Notes are indexed for "
        "semantic search and tracked for provenance and staleness."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "path": {
                "type": "string",
                "description": "Relative path within the vault (e.g. 'notes/topic.md').",
            },
            "content": {
                "type": "string",
                "description": "Markdown content to write.",
            },
            "append": {
                "type": "boolean",
                "description": "Append to existing file instead of overwriting.",
                "default": False,
            },
        },
        "required": ["path", "content", "append"],
    },
}

SAVE_DECISION_SCHEMA = {
    "name": "same_save_decision",
    "description": (
        "Log a project decision to the SAME decision log. Decisions are "
        "timestamped, attributed, and searchable by future sessions."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "title": {
                "type": "string",
                "description": "Short decision title (e.g. 'Use JWT for auth').",
            },
            "body": {
                "type": "string",
                "description": "Full decision details — what was decided, why, alternatives.",
            },
            "status": {
                "type": "string",
                "description": "Decision status: 'accepted', 'proposed', or 'superseded'.",
                "enum": ["accepted", "proposed", "superseded"],
            },
        },
        "required": ["title", "body"],
    },
}

GET_NOTE_SCHEMA = {
    "name": "same_get_note",
    "description": (
        "Read the full content of a note from the SAME vault by path."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "path": {
                "type": "string",
                "description": "Relative path from vault root.",
            },
        },
        "required": ["path"],
    },
}

HEALTH_SCHEMA = {
    "name": "same_health",
    "description": (
        "Check SAME vault health — index coverage, stale notes, "
        "consolidation opportunities, and trust state distribution."
    ),
    "parameters": {
        "type": "object",
        "properties": {},
    },
}


# ---------------------------------------------------------------------------
# Security: secret redaction and injection filtering for prefetch
# ---------------------------------------------------------------------------

_SECRET_PATTERNS = [
    re.compile(r'sk-ant-[a-zA-Z0-9_-]{20,}'),      # Anthropic
    re.compile(r'sk-[a-zA-Z0-9]{20,}'),              # OpenAI
    re.compile(r'fc-[a-zA-Z0-9]{32,}'),              # Firecrawl
    re.compile(r'ghp_[a-zA-Z0-9]{36}'),              # GitHub PAT
    re.compile(r'gho_[a-zA-Z0-9]{36}'),              # GitHub OAuth
    re.compile(r'AKIA[0-9A-Z]{16}'),                 # AWS access key
    re.compile(r'eyJ[a-zA-Z0-9_-]{20,}\.[a-zA-Z0-9_-]{20,}\.[a-zA-Z0-9_-]{20,}'),  # JWT
    re.compile(r'xox[bpors]-[a-zA-Z0-9-]{10,}'),    # Slack token
]

_INJECTION_PATTERNS = [
    re.compile(r'ignore\s+(all\s+)?(previous|prior)\s+instructions', re.IGNORECASE),
    re.compile(r'you\s+are\s+now\b', re.IGNORECASE),
    re.compile(r'your\s+new\s+(task|role|instructions)', re.IGNORECASE),
    re.compile(r'\[INST\]', re.IGNORECASE),
    re.compile(r'<SYSTEM>', re.IGNORECASE),
    re.compile(r'system\s*prompt\s*:', re.IGNORECASE),
    re.compile(r'IMPORTANT\s+SYSTEM\s+INSTRUCTIONS', re.IGNORECASE),
]


def _redact_secrets(text: str) -> str:
    """Replace known secret patterns with [REDACTED]."""
    try:
        from agent.redact import redact
        return redact(text, full=True)
    except (ImportError, TypeError):
        pass
    for pattern in _SECRET_PATTERNS:
        text = pattern.sub('[REDACTED]', text)
    return text


def _looks_like_injection(text: str) -> bool:
    """Check if text contains prompt injection patterns."""
    return any(p.search(text) for p in _INJECTION_PATTERNS)


def _extract_text(content) -> str:
    """Extract text from a message content field.

    Handles both plain strings and Anthropic-style content block lists:
    [{"type": "text", "text": "..."}, {"type": "tool_use", ...}]
    """
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return " ".join(
            block.get("text", "")
            for block in content
            if isinstance(block, dict) and block.get("type") == "text"
        )
    return ""


# ---------------------------------------------------------------------------
# Config loading
# ---------------------------------------------------------------------------

def _load_config() -> dict:
    """Load SAME plugin config from profile-scoped path or env vars.

    Resolution order:
      1. $HERMES_HOME/same/config.json  (profile-scoped)
      2. Environment variables
    """
    try:
        from hermes_constants import get_hermes_home
        config_path = get_hermes_home() / "same" / "config.json"
        if config_path.exists():
            return json.loads(config_path.read_text(encoding="utf-8"))
    except Exception:
        pass

    return {
        "vault_path": os.environ.get("SAME_VAULT_PATH", ""),
        "binary": os.environ.get("SAME_BINARY", "same"),
        "agent": os.environ.get("SAME_AGENT", "hermes"),
    }


# ---------------------------------------------------------------------------
# MemoryProvider implementation
# ---------------------------------------------------------------------------

class SAMEMemoryProvider(MemoryProvider):
    """SAME — cross-agent memory with provenance and trust state."""

    def __init__(self):
        self._config: dict = {}
        self._vault_path = ""
        self._binary = "same"
        self._agent = "hermes"
        self._mcp: MCPStdioClient | None = None
        self._session_id = ""
        self._agent_context = "primary"
        self._prefetch_result = ""
        self._prefetch_lock = threading.Lock()
        self._prefetch_thread: threading.Thread | None = None

    @property
    def name(self) -> str:
        return "same"

    # -- Core lifecycle -------------------------------------------------------

    def is_available(self) -> bool:
        cfg = _load_config()
        vault = cfg.get("vault_path", "")
        binary = cfg.get("binary", "same")
        if not vault:
            logger.warning(
                "SAME plugin: SAME_VAULT_PATH not set. "
                "Set it in ~/.hermes/.env or run 'hermes memory setup'."
            )
            return False
        if not shutil.which(binary):
            logger.warning(
                "SAME plugin: '%s' not found on PATH. "
                "Install from https://statelessagent.com or set SAME_BINARY.", binary
            )
            return False
        return True

    def initialize(self, session_id: str, **kwargs) -> None:
        self._config = _load_config()
        self._vault_path = self._config.get("vault_path", "")
        self._binary = self._config.get("binary", "same")
        self._agent = self._config.get("agent", "hermes")
        self._session_id = session_id
        self._agent_context = kwargs.get("agent_context", "primary")

        # Start persistent MCP subprocess
        env = {"VAULT_PATH": self._vault_path}
        self._mcp = MCPStdioClient(self._binary, ["mcp"], env=env)
        try:
            self._mcp.start(timeout=15.0)
        except Exception as e:
            logger.error("Failed to start SAME MCP server: %s", e)
            self._mcp = None
            raise

        # Check for .sameignore — without it, vault noise dominates search results
        sameignore_path = Path(self._vault_path) / ".sameignore"
        if not sameignore_path.exists():
            logger.warning(
                "SAME plugin: No .sameignore found at %s. Search results may be "
                "dominated by framework docs and dependencies. Copy a template: "
                "cp templates/sameignore/hermes-agent.sameignore %s/.sameignore && same reindex",
                self._vault_path, self._vault_path,
            )

        # Version check: compare MCP server version against CLI binary version.
        # A mismatch means the running binary and the installed CLI are out of sync,
        # which can cause subtle incompatibilities. We warn but never raise — degraded
        # operation is better than a hard failure.
        self._check_version_mismatch()

    def _check_version_mismatch(self) -> None:
        """Compare the MCP server version (from the initialize handshake) against
        the CLI binary version (from 'same --version'). If they differ it means
        the user has multiple same installations out of sync. Log a WARNING with
        an actionable fix. Never raises — degraded operation is better than none.
        """
        if not self._mcp:
            return

        mcp_version = self._mcp.server_version
        if not mcp_version:
            # Server didn't report a version — skip the check silently.
            return

        try:
            result = subprocess.run(
                [self._binary, "--version"],
                capture_output=True,
                text=True,
                timeout=5,
                env={
                    "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
                    "HOME": os.environ.get("HOME", ""),
                },
            )
            cli_output = (result.stdout or result.stderr or "").strip()
            # "same --version" typically prints "same version 1.2.3" or just "1.2.3"
            # Extract the last whitespace-separated token that looks like a version.
            tokens = cli_output.split()
            cli_version = tokens[-1] if tokens else ""
        except Exception as e:
            logger.debug("SAME version check: could not run '%s --version': %s", self._binary, e)
            return

        if not cli_version:
            return

        if mcp_version != cli_version:
            logger.warning(
                "SAME binary version mismatch: MCP server reports '%s' but "
                "'%s --version' reports '%s'. This may cause unexpected behaviour. "
                "Run: same update",
                mcp_version, self._binary, cli_version,
            )
        else:
            logger.debug("SAME version check OK: both MCP server and CLI report '%s'", mcp_version)

    def system_prompt_block(self) -> str:
        return (
            "# SAME Memory\n"
            "Active. Cross-agent memory vault with provenance tracking.\n"
            "Use same_search to find prior context, decisions, and handoffs.\n"
            "Use same_save_decision to log important decisions.\n"
            "Use same_save_note to persist knowledge.\n"
            "Use same_get_note to read full note content.\n"
            "Use same_health to check vault status."
        )

    def prefetch(self, query: str, *, session_id: str = "") -> str:
        if self._prefetch_thread and self._prefetch_thread.is_alive():
            self._prefetch_thread.join(timeout=3.0)
        with self._prefetch_lock:
            result = self._prefetch_result
            self._prefetch_result = ""
        if not result:
            return ""
        return f"## SAME Memory (auto-recall)\n{result}"

    def queue_prefetch(self, query: str, *, session_id: str = "") -> None:
        if not self._mcp or not self._mcp.is_alive():
            return

        def _run():
            try:
                raw = self._mcp.call_tool("search_notes", {
                    "query": query,
                    "top_k": 5,
                }, timeout=10.0)
                # Parse and filter the search results
                try:
                    results = json.loads(raw)
                    if isinstance(results, list) and results:
                        lines = []
                        for r in results[:5]:
                            snippet = r.get("snippet", "")[:200]
                            # B-03/B-04: Skip notes that look like injection
                            if _looks_like_injection(snippet):
                                logger.warning("SAME prefetch: skipped injection-like note: %s",
                                               r.get("path", "unknown"))
                                continue
                            title = r.get("title", r.get("path", "untitled"))
                            trust = r.get("trust_state", "unknown")
                            trust_tag = " ⚠" if trust in ("stale", "contradicted") else ""
                            # B-05: Redact secrets before injecting into context
                            snippet = _redact_secrets(snippet)
                            lines.append(f"- **{title}**{trust_tag}: {snippet}")
                        text = "\n".join(lines)
                    elif isinstance(results, dict) and results.get("error"):
                        text = ""
                    else:
                        text = raw[:1000] if raw and "No results" not in raw else ""
                except json.JSONDecodeError:
                    text = raw[:1000] if raw and "No results" not in raw and "Error" not in raw else ""
                if text:
                    text = _redact_secrets(text)
                with self._prefetch_lock:
                    self._prefetch_result = text
            except Exception as e:
                logger.debug("SAME prefetch failed: %s", e)

        self._prefetch_thread = threading.Thread(
            target=_run, daemon=True, name="same-prefetch"
        )
        self._prefetch_thread.start()

    def sync_turn(self, user_content: str, assistant_content: str, *, session_id: str = "") -> None:
        # Intentionally no-op: SAME prefers explicit saves (decisions, notes)
        # over auto-dumping every conversation turn. The agent uses
        # same_save_note and same_save_decision when it wants to remember.
        pass

    def get_tool_schemas(self) -> List[Dict[str, Any]]:
        return [
            SEARCH_SCHEMA,
            SAVE_NOTE_SCHEMA,
            SAVE_DECISION_SCHEMA,
            GET_NOTE_SCHEMA,
            HEALTH_SCHEMA,
        ]

    def handle_tool_call(self, tool_name: str, args: Dict[str, Any], **kwargs) -> str:
        if not self._mcp or not self._mcp.is_alive():
            return json.dumps({"error": "SAME MCP server not running"})

        # Map Hermes tool names to SAME MCP tool names
        tool_map = {
            "same_search": "search_notes",
            "same_save_note": "save_note",
            "same_save_decision": "save_decision",
            "same_get_note": "get_note",
            "same_health": "mem_health",
        }

        mcp_tool = tool_map.get(tool_name)
        if not mcp_tool:
            return json.dumps({"error": f"Unknown tool: {tool_name}"})

        # Inject defaults for required params the server expects
        if tool_name == "same_search":
            args.setdefault("top_k", 10)
        elif tool_name == "same_save_note":
            args.setdefault("append", False)
        elif tool_name == "same_save_decision":
            args.setdefault("status", "accepted")

        # Inject agent attribution for write operations
        if tool_name in ("same_save_note", "same_save_decision"):
            if "agent" not in args:
                args["agent"] = self._agent

        try:
            return self._mcp.call_tool(mcp_tool, args, timeout=30.0)
        except Exception as e:
            return json.dumps({"error": f"SAME tool call failed: {e}"})

    def shutdown(self) -> None:
        if self._prefetch_thread and self._prefetch_thread.is_alive():
            self._prefetch_thread.join(timeout=5.0)
        if self._mcp:
            self._mcp.close()
            self._mcp = None

    # -- Optional hooks -------------------------------------------------------

    def on_session_end(self, messages: List[Dict[str, Any]]) -> None:
        """Create a SAME handoff note summarizing the session."""
        if self._agent_context != "primary":
            return
        if not self._mcp or not self._mcp.is_alive():
            return

        # Collect user requests and assistant responses for context
        user_msgs = []
        assistant_msgs = []
        for m in messages:
            role = m.get("role", "")
            content = _extract_text(m.get("content", ""))
            if not content.strip():
                continue
            if role == "user":
                user_msgs.append(content.strip()[:300])
            elif role == "assistant":
                assistant_msgs.append(content.strip()[:300])

        if not user_msgs and not assistant_msgs:
            return

        # Build structured summary: what was asked, what was done
        summary_lines = []

        # What the user asked for (deduplicated topics)
        if user_msgs:
            summary_lines.append("**What was requested:**")
            for msg in user_msgs[-8:]:
                first_line = msg.split("\n")[0][:200]
                if first_line and len(first_line) > 10:
                    summary_lines.append(f"- {first_line}")

        # What was accomplished (last few assistant outputs)
        if assistant_msgs:
            summary_lines.append("\n**What was done:**")
            for msg in assistant_msgs[-5:]:
                # Take substantial lines, skip code blocks and short fragments
                for line in msg.split("\n"):
                    line = line.strip()
                    if (line and len(line) > 30
                            and not line.startswith("```")
                            and not line.startswith("|")
                            and not line.startswith("//")
                            and not line.startswith("#!")):
                        summary_lines.append(f"- {line[:200]}")
                        break

        summary = "\n".join(summary_lines)

        # Also extract pending items from the last assistant message
        pending = ""
        if assistant_msgs:
            last = assistant_msgs[-1]
            # Look for TODO, next step, remaining, pending patterns
            for line in last.split("\n"):
                lower = line.lower().strip()
                if any(kw in lower for kw in ["todo", "next step", "remaining", "pending", "need to", "should"]):
                    pending += line.strip()[:200] + "\n"

        try:
            args = {
                "summary": summary,
                "agent": self._agent,
            }
            if pending.strip():
                args["pending"] = pending.strip()
            self._mcp.call_tool("create_handoff", args, timeout=15.0)
        except Exception as e:
            logger.debug("SAME handoff creation failed: %s", e)

    def on_pre_compress(self, messages: List[Dict[str, Any]]) -> str:
        """Extract key facts from messages about to be compressed."""
        if not self._mcp or not self._mcp.is_alive():
            return ""

        # Collect user and assistant content from messages being compressed
        content_parts = []
        for m in messages:
            role = m.get("role", "")
            content = _extract_text(m.get("content", ""))
            if role in ("user", "assistant") and content.strip():
                content_parts.append(f"{role}: {content[:500]}")

        if not content_parts:
            return ""

        # Save a compression summary note so context isn't lost
        combined = "\n\n".join(content_parts[:10])
        try:
            self._mcp.call_tool("save_note", {
                "path": f"sessions/compressed-{self._session_id[:8]}.md",
                "content": f"# Compressed Context\n\nSession: {self._session_id}\n\n{combined}",
                "agent": self._agent,
            }, timeout=15.0)
        except Exception as e:
            logger.debug("SAME pre-compress save failed: %s", e)

        return "SAME vault has been updated with compressed context."

    def on_memory_write(self, action: str, target: str, content: str) -> None:
        """Mirror built-in memory writes to the SAME vault."""
        if self._agent_context != "primary":
            return
        if not self._mcp or not self._mcp.is_alive():
            return
        if action == "remove":
            return  # Don't mirror removals

        # Append to a single file per target to avoid vault pollution
        target_file = "memory/hermes-memory.md" if target == "memory" else "profiles/hermes-profile.md"
        timestamp = time.strftime("%Y-%m-%d %H:%M")
        entry = f"\n\n## {action.title()} — {timestamp}\n\n{content}"

        def _mirror():
            try:
                self._mcp.call_tool("save_note", {
                    "path": target_file,
                    "content": entry,
                    "append": True,
                    "agent": self._agent,
                }, timeout=10.0)
            except Exception as e:
                logger.debug("SAME memory mirror failed: %s", e)

        threading.Thread(target=_mirror, daemon=True, name="same-mirror").start()

    def on_turn_start(self, turn_number: int, message: str, **kwargs) -> None:
        """Restart MCP client if it died between turns."""
        if self._mcp and not self._mcp.is_alive():
            logger.warning("SAME MCP server died, restarting...")
            try:
                env = {"VAULT_PATH": self._vault_path}
                self._mcp = MCPStdioClient(self._binary, ["mcp"], env=env)
                self._mcp.start(timeout=10.0)
            except Exception as e:
                logger.error("Failed to restart SAME MCP server: %s", e)
                self._mcp = None

    # -- Config/setup ---------------------------------------------------------

    def get_config_schema(self) -> List[Dict[str, Any]]:
        return [
            {
                "key": "vault_path",
                "description": "Path to your SAME vault directory",
                "required": True,
                "env_var": "SAME_VAULT_PATH",
            },
            {
                "key": "binary",
                "description": "Path to the same binary",
                "default": "same",
                "env_var": "SAME_BINARY",
            },
            {
                "key": "agent",
                "description": "Agent name for write attribution",
                "default": "hermes",
                "env_var": "SAME_AGENT",
            },
        ]

    def save_config(self, values: Dict[str, Any], hermes_home: str) -> None:
        """Write config to $HERMES_HOME/same/config.json."""
        config_dir = Path(hermes_home) / "same"
        config_dir.mkdir(parents=True, exist_ok=True)
        config_path = config_dir / "config.json"
        existing = {}
        if config_path.exists():
            try:
                existing = json.loads(config_path.read_text(encoding="utf-8"))
            except Exception:
                pass
        existing.update(values)
        config_path.write_text(json.dumps(existing, indent=2), encoding="utf-8")


# ---------------------------------------------------------------------------
# Plugin registration
# ---------------------------------------------------------------------------

def register(ctx) -> None:
    """Register SAME as a memory provider plugin."""
    ctx.register_memory_provider(SAMEMemoryProvider())
