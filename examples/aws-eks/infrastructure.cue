package main

import (
	"encoding/json"

	"dagger.io/aws"
	"dagger.io/aws/cloudformation"
)

#Infrastructure: {
	awsConfig:              aws.#Config
	namePrefix:             *"" | string
	workerNodeCapacity:     *3 | >=1
	workerNodeInstanceType: *"t3.medium" | string

	let clusterName = "\(namePrefix)eks-cluster"

	eksControlPlane: cloudformation.#Stack & {
		config:    awsConfig
		source:    json.Marshal(#CFNTemplate.eksControlPlane)
		stackName: "\(namePrefix)eks-controlplane"
		neverUpdate: true
		timeout: 30
		parameters: ClusterName: clusterName
	}

	eksNodeGroup: cloudformation.#Stack & {
		config:    awsConfig
		source:    json.Marshal(#CFNTemplate.eksNodeGroup)
		stackName: "\(namePrefix)eks-nodegroup"
		neverUpdate: true
		timeout: 30
		parameters: {
			ClusterName:                         clusterName
			NodeAutoScalingGroupDesiredCapacity: 1
			NodeAutoScalingGroupMaxSize:         NodeAutoScalingGroupDesiredCapacity + 1
			NodeInstanceType:                    workerNodeInstanceType
			Subnets:                             eksControlPlane.outputs.SubnetIds
		}
	}
}
