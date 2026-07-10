FROM python:3.13-alpine

WORKDIR /app
COPY sdk/python/stormrelay_plugin /app/stormrelay_plugin
COPY examples/plugins/echo-python-sdk/plugin.py /app/plugin.py

ENV PYTHONPATH=/app
EXPOSE 8090
USER 65532:65532
ENTRYPOINT ["python", "/app/plugin.py"]
