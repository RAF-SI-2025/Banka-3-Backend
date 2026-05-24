FROM spark:python3

USER root

ADD https://jdbc.postgresql.org/download/postgresql-42.7.5.jar /tmp/postgresql-42.7.5.jar
RUN cp /tmp/postgresql-42.7.5.jar /opt/spark/jars/postgresql-42.7.5.jar \
    && chmod 644 /opt/spark/jars/postgresql-42.7.5.jar
RUN python3 -m pip install --no-cache-dir numpy

USER spark

COPY analytics/spark/jobs /opt/banka-analytics/jobs
