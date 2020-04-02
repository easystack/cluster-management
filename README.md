# cluster-management

This project leveraged Kubernetes Operator to help manage multiple EOS clusters

- CRD "eoscluster" to store Cluster Info
- Controller
  - Polling latest cluster info to update CRD 
  - create/delete namespaces, network policy, pod security policy


## Quick Start

- Precondition: you’ll need a Kubernetes cluster to run as Supervisor Cluster, and clusters to be managed.<br>
  ***Notes***:
  * Make sure SuperVisor could access k8s api on managed clusters, it need add *public_ip* into openssl.conf and regenerate certificates
  * Make sure managed clusters could access keystone on Supervisor cluster.    

- Download Code on Supervisor cluster

  ```git clone git@github.com:es-container/cluster-management.git```

- Run cluster-management

  ```make install ``` to install CRD<br>
  ```make run``` to run controller as process on host
  
- Function Demo

  * Create: <br>
  ```kubectl apply -f config/samples/eoscluster_with_host.yaml``` to create eoscluster without projects <br>
  ```kubectl apply -f config/samples/eoscluster_with_host.yaml``` to create eoscluster with projects
  
  * Update: <br>
  ```kubectl edit eoscluster eoscluster-host``` to update projects list
  
  * Delete: <br>
  ```kubectl delete eoscluster eoscluster-host``` to delete eoscluster
  
   ***Notes***:
   ```kubctl get eoscluster eoscluster-host -o yaml ``` to check cluster info was updated as expect.
   Log into managed clusters to check the namespaces(same name with projects) were created/deleted as expect.
   
   * Polling: <br>
   ```systemctl stop kubelet``` to imitate nodes NotReady
   ```kubctl get eoscluster eoscluster-host -o yaml ``` to check cluster status was updated as expect.