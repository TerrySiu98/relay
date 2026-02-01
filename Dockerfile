FROM alpine:latest
ARG TARGETARCH
WORKDIR /data
COPY ${TARGETARCH}/relay_linux_${TARGETARCH} /usr/local/bin/relay
RUN chmod +x /usr/local/bin/relay
EXPOSE 8888 9999
ENTRYPOINT ["relay"]
CMD ["-mode", "master"]