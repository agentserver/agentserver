import pytest

from agentserver_sdk.ctx import Ctx


async def test_envs_returns_parsed_list(stub_client):
    client, stub = stub_client
    ctx = Ctx(client)

    async def envs(body, query):
        return 200, {
            "envs": [
                {
                    "name": "my-mac",
                    "type": "executor",
                    "is_default": True,
                    "tools": [{"name": "shell", "description": "...", "kind": "core"}],
                }
            ]
        }

    stub.register("POST", "/api/connectors/envs/list", envs)
    result = await ctx.envs()
    assert len(result) == 1
    assert result[0].name == "my-mac"
    assert result[0].tools[0].name == "shell"


async def test_envs_hits_gateway_every_call(stub_client):
    """Each envs() call must hit the gateway — there is no client-side cache,
    because executors come and go and only the gateway knows the truth."""
    client, stub = stub_client
    ctx = Ctx(client)
    calls = {"n": 0}

    async def envs(body, query):
        calls["n"] += 1
        return 200, {"envs": []}

    stub.register("POST", "/api/connectors/envs/list", envs)
    await ctx.envs()
    await ctx.envs()
    await ctx.envs()
    assert calls["n"] == 3


async def test_env_by_name_returns_matching_env(stub_client):
    client, stub = stub_client
    ctx = Ctx(client)

    async def envs(body, query):
        return 200, {
            "envs": [
                {"name": "alpha", "type": "shell", "tools": []},
                {"name": "hpc", "type": "hpc", "tools": []},
            ]
        }

    stub.register("POST", "/api/connectors/envs/list", envs)
    alpha = await ctx.env("alpha")
    assert alpha.name == "alpha"
    assert alpha.type == "shell"


async def test_env_by_name_missing_raises_key_error(stub_client):
    client, stub = stub_client
    ctx = Ctx(client)

    async def envs(body, query):
        return 200, {"envs": []}

    stub.register("POST", "/api/connectors/envs/list", envs)
    with pytest.raises(KeyError):
        await ctx.env("nope")


async def test_from_env_reads_env_vars(monkeypatch, stub_client):
    client, stub = stub_client
    monkeypatch.setenv("AGENTSERVER_GATEWAY_URL", "http://stub")
    monkeypatch.setenv("AGENTSERVER_WORKSPACE_TOKEN", "tok")
    ctx = Ctx.from_env()
    assert ctx._client.base_url == "http://stub"
    await ctx.close()
