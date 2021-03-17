package main

import (
	"encoding/json"

	"dagger.io/aws"
	"dagger.io/aws/cloudformation"
)

#Infrastructure: {
	awsConfig:  aws.#Config
	namePrefix: *"dagger-example-" | string
	// Cluster size is 1 for example (to limit resources)
	workerNodeCapacity:     *1 | >1
	workerNodeInstanceType: *"t3.small" | string

	let clusterName = "\(namePrefix)eks-cluster"

	eksControlPlane: cloudformation.#Stack & {
		config:    awsConfig
		source:    json.Marshal(#CFNTemplate.eksControlPlane)
		stackName: "\(namePrefix)eks-controlplane"
		neverUpdate: true
		parameters: ClusterName: clusterName
	}

	eksNodeGroup: cloudformation.#Stack & {
		config:    awsConfig
		source:    json.Marshal(#CFNTemplate.eksNodeGroup)
		stackName: "\(namePrefix)eks-nodegroup"
		neverUpdate: true
		parameters: {
			ClusterName:                         clusterName
			ClusterControlPlaneSecurityGroup:    eksControlPlane.outputs.DefaultSecurityGroup
			NodeAutoScalingGroupDesiredCapacity: 1
			NodeAutoScalingGroupMaxSize:         NodeAutoScalingGroupDesiredCapacity + 1
			NodeGroupName:                       "\(namePrefix)eks-nodegroup"
			NodeInstanceType:                    workerNodeInstanceType
			VpcId:                               eksControlPlane.outputs.VPC
			Subnets:                             eksControlPlane.outputs.SubnetIds
		}
	}
}
