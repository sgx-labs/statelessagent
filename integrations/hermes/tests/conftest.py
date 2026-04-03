"""Shared pytest fixtures for the SAME Hermes memory provider tests.

Requires:
  - SAME binary on PATH (or SAME_BINARY env var pointing to it)
  - SAME_VAULT_PATH pointing to an initialized vault (eval/test_vault)
  - Python path includes the hermes-agent package (for agent.memory_provider)

Run with:
  PYTHONPATH=/path/to/hermes-agent pytest integrations/hermes/tests/ -v
"""

from __future__ import annotations

import os
import sys
import time
import json
import importlib.util
import pytest

# ---------------------------------------------------------------------------
# Path setup — find the hermes-agent package so we can import agent.*
# ---------------------------------------------------------------------------

_HERMES_AGENT_CANDIDATES = [
    os.path.expanduser("~/code/hermes-agent"),
    "/opt/hermes-agent",
    "/usr/local/lib/hermes-agent",
]

def _setup_hermes_agent_path() -> None:
    """Add hermes-agent to sys.path if not already importable."""
    try:
        import agent.memory_provider  # noqa: F401
        return  # already importable
    except ImportError:
        pass
    for candidate in _HERMES_AGENT_CANDIDATES:
        if os.path.isdir(candidate) and os.path.isfile(
            os.path.join(candidate, "agent", "memory_provider.py")
        ):
            sys.path.insert(0, candidate)
            return
    # Also check PYTHONPATH env
    for p in os.environ.get("PYTHONPATH", "").split(os.pathsep):
        if p and os.path.isfile(os.path.join(p, "agent", "memory_provider.py")):
            sys.path.insert(0, p)
            return

_setup_hermes_agent_path()

# ---------------------------------------------------------------------------
# Project root — make integrations.hermes importable
# ---------------------------------------------------------------------------

_PROJECT_ROOT = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "..")
)
if _PROJECT_ROOT not in sys.path:
    sys.path.insert(0, _PROJECT_ROOT)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

VAULT_PATH = os.path.abspath(
    os.path.join(_PROJECT_ROOT, "eval", "test_vault")
)

SAME_BINARY = os.environ.get(
    "SAME_BINARY",
    os.path.expanduser("~/same-new") if os.path.isfile(os.path.expanduser("~/same-new"))
    else "same"
)

# ---------------------------------------------------------------------------
# Session-scoped: one live provider for the entire test run
# ---------------------------------------------------------------------------

@pytest.fixture(scope="session")
def vault_path() -> str:
    return VAULT_PATH


@pytest.fixture(scope="session")
def same_binary() -> str:
    return SAME_BINARY


@pytest.fixture(scope="session")
def live_provider(vault_path, same_binary):
    """Start a real SAMEMemoryProvider against the test vault.

    Session-scoped so we pay the MCP startup cost only once.
    """
    os.environ["SAME_VAULT_PATH"] = vault_path
    os.environ["SAME_BINARY"] = same_binary

    from integrations.hermes import SAMEMemoryProvider

    provider = SAMEMemoryProvider()
    provider.initialize("pytest-session-001")
    yield provider
    provider.shutdown()


@pytest.fixture()
def provider(vault_path, same_binary):
    """Per-test provider — fresh MCP session each time.

    Use this for tests that mutate provider state (prefetch cache, mcp proc).
    """
    os.environ["SAME_VAULT_PATH"] = vault_path
    os.environ["SAME_BINARY"] = same_binary

    from integrations.hermes import SAMEMemoryProvider

    p = SAMEMemoryProvider()
    p.initialize(f"pytest-test-{int(time.time())}")
    yield p
    try:
        p.shutdown()
    except Exception:
        pass
