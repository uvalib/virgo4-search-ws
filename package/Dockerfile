#
# build the target application
#
FROM public.ecr.aws/docker/library/golang:1.21.6-alpine3.19 as builder
RUN apk update && apk upgrade && apk add --no-cache make

WORKDIR /build
COPY go.mod go.sum Makefile ./
COPY cmd ./cmd
COPY i18n ./i18n
COPY ttf ./ttf
RUN make linux

#
# build the target container
#
FROM public.ecr.aws/docker/library/alpine:3.19

# update the packages
RUN apk update && apk upgrade && apk add bash tzdata ca-certificates && rm -rf /var/cache/apk/*

# Create the run user and group
RUN addgroup webservice && adduser webservice -G webservice -D

# set the timezone appropriatly
ENV TZ=UTC
RUN cp /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

# Specify home
ENV APP_HOME /v4-master-search-ws
WORKDIR $APP_HOME

# Create necessary directories
RUN mkdir -p $APP_HOME $APP_HOME/bin $APP_HOME/bin/i18n $APP_HOME/bin/ttf $APP_HOME/scripts
RUN chown -R webservice $APP_HOME && chgrp -R webservice $APP_HOME

# port and run command
EXPOSE 8080
CMD scripts/entry.sh

# Move in necessary assets
COPY package/data/container_bash_profile /home/webservice/.profile
COPY package/scripts/entry.sh $APP_HOME/scripts/entry.sh
COPY i18n $APP_HOME/bin/i18n
COPY ttf $APP_HOME/bin/ttf
COPY --from=builder /build/bin/v4search.linux $APP_HOME/bin/v4search
COPY i18n/* $APP_HOME/bin/i18n/
COPY ttf/* $APP_HOME/bin/ttf/


# Ensure permissions are correct
RUN chown webservice:webservice /home/webservice/.profile $APP_HOME/scripts/entry.sh $APP_HOME/bin/v4search $APP_HOME/bin/i18n $APP_HOME/bin/ttf && chmod 755 /home/webservice/.profile $APP_HOME/scripts/entry.sh $APP_HOME/bin/v4search $APP_HOME/bin/i18n $APP_HOME/bin/ttf

# Add the build tag
ARG BUILD_TAG
RUN test -n "$BUILD_TAG" && touch $APP_HOME/buildtag.build-$BUILD_TAG || touch $APP_HOME/buildtag.build-0

# Specify the user
USER webservice

#
# end of file
#
