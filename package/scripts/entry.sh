# define options
AWS_KEY_OPT=""
AWS_SECRET_OPT=""
AWS_REGION_OPT=""

# AWS access key
if [ -n "$V4_AWS_ACCESS" ]; then
   AWS_KEY_OPT="-aws_access $V4_AWS_ACCESS"
fi

# AWS secret key
if [ -n "$V4_AWS_SECRET" ]; then
   AWS_SECRET_OPT="-aws_secret $V4_AWS_SECRET"
fi

# AWS region
if [ -n "$V4_AWS_REGION" ]; then
   AWS_REGION_OPT="-aws_region $V4_AWS_REGION"
fi

# run application

# run from here
cd bin; ./v4search $AWS_KEY_OPT $AWS_SECRET_OPT $AWS_REGION_OPT -ddb_table $V4_DYNAMO_DB_TABLE

#
# end of file
#
