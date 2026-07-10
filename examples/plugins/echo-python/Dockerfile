FROM python:3.13-alpine
RUN addgroup -S -g 65532 plugin && adduser -S -D -H -u 65532 -G plugin plugin
COPY plugin.py /plugin.py
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["python", "/plugin.py"]
