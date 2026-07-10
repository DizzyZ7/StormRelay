from __future__ import annotations

import os

from stormrelay_plugin import Action, Plugin, PluginRequest, fail


def echo(request: PluginRequest) -> dict:
    return {"echo": request.input}


def require_message(request: PluginRequest) -> dict:
    if not isinstance(request.input, dict) or not isinstance(request.input.get("message"), str):
        raise fail("input.message must be a string")
    return {"message": request.input["message"]}


plugin = Plugin(
    "echo-python-sdk",
    "1.0.0",
    [
        Action("echo", echo, description="Returns the input unchanged."),
        Action(
            "require-message",
            require_message,
            description="Validates and returns input.message.",
        ),
    ],
    bearer_token=os.getenv("PLUGIN_BEARER_TOKEN"),
)


if __name__ == "__main__":
    plugin.serve(port=int(os.getenv("PORT", "8090")))
