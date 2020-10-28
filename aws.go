package main

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/servicediscovery"
	"github.com/rs/zerolog/log"
)

func getAwsSession() (sess *session.Session, err error) {
	sess, err = session.NewSession()
	if err != nil {
		return nil, err
	}

	stsAssumeRoleArn := os.Getenv("AWS_STS_ASSUME_ROLE_ARN")
	if stsAssumeRoleArn != "" {
		sess.Config.Credentials = stscreds.NewCredentials(sess, stsAssumeRoleArn)
	}
	return
}

type CloudMapServiceUplooker struct {
	sd *servicediscovery.ServiceDiscovery
}

func NewCloudMapServiceUplooker(sess *session.Session) *CloudMapServiceUplooker {
	return &CloudMapServiceUplooker{
		sd: servicediscovery.New(sess),
	}
}

func toFields(fields map[string]*string) map[string]interface{} {
	retval := make(map[string]interface{})
	for k, v := range fields {
		retval[k] = v
	}
	return retval
}

func buildServiceInstanceFromInstance(id string, attrs map[string]*string) (si ServiceInstance, err error) {
	si.InstanceId = id
	if healthStatus, ok := attrs["AWS_INIT_HEALTH_STATUS"]; ok && healthStatus != nil && *healthStatus == "HEALTHY" {
		si.Healthy = true
	}
	if ipv4AddrRepr, ok := attrs["AWS_INSTANCE_IPV4"]; ok && ipv4AddrRepr != nil {
		si.V4Addr, err = net.ResolveIPAddr("ip4", *ipv4AddrRepr)
		if err != nil {
			err = fmt.Errorf("failed to parse the value of attribute AWS_INSTANCE_IPV4 as IPv4 address: %w", err)
			return
		}
	}
	if ipv6AddrRepr, ok := attrs["AWS_INSTANCE_IPV6"]; ok && ipv6AddrRepr != nil {
		si.V6Addr, err = net.ResolveIPAddr("ip6", *ipv6AddrRepr)
		if err != nil {
			err = fmt.Errorf("failed to parse the value of attribute AWS_INSTANCE_IPV6 as IPv6 address: %w", err)
			return
		}
	}
	si.Attributes = make(map[string]string)
	for k, v := range attrs {
		si.Attributes[k] = *v
	}
	return
}

func buildServiceDescriptorFromInstances(namespaceName, serviceName string, instances []*servicediscovery.HttpInstanceSummary) (sd *ServiceDescriptor, err error) {
	sd = &ServiceDescriptor{
		NamespaceName: namespaceName,
		ServiceName:   serviceName,
	}
	sd.Instances = make([]ServiceInstance, len(instances))
	for i, ii := range instances {
		sd.Instances[i], err = buildServiceInstanceFromInstance(*ii.InstanceId, ii.Attributes)
		if err != nil {
			return
		}
	}
	return
}

func (ul *CloudMapServiceUplooker) LookupService(ctx context.Context, namespaceName, serviceName string) (sd *ServiceDescriptor, err error) {
	if log.Debug().Enabled() {
		log.Debug().Str("namespace_name", namespaceName).Str("service_name", serviceName).Msg("looking up service")
	}
	diOut, err := ul.sd.DiscoverInstancesWithContext(ctx, &servicediscovery.DiscoverInstancesInput{NamespaceName: &namespaceName, ServiceName: &serviceName, HealthStatus: aws.String(servicediscovery.HealthStatusFilterHealthy)})
	if err != nil {
		return
	}
	sd, err = buildServiceDescriptorFromInstances(serviceName, serviceName, diOut.Instances)
	if err != nil {
		return
	}
	return
}
