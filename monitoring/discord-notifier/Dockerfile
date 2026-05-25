FROM python:3.13-alpine

WORKDIR /app

COPY monitoring/discord-notifier/server.py /app/server.py

ENTRYPOINT ["python", "/app/server.py"]
