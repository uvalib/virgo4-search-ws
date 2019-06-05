# run application

PWD_OPTION=""
if [ -n "$V4_SEARCH_REDIS_PASS" ]; then
   PWD_OPTION="-redis_pass $V4_SEARCH_REDIS_PASS"
fi

# run from here, since application expects json template in templates/
cd bin; ./v4search -redis_host $V4_SEARCH_REDIS_HOST -redis_port $V4_SEARCH_REDIS_PORT -redis_prefix $V4_SEARCH_REDIS_PREFIX -redis_db $V4_SEARCH_REDIS_DB $PWD_OPTION

#
# end of file
#
