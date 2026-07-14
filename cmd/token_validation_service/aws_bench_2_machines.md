## SSH 
Add ssh pubkey of server to client `known_hosts`

## Server
```bash
GOGC=10000 go run ./server/
```

## Client 
1. Rsync:

```bash
rsync -avz AWS-128:~/effi/panurus/cmd/token_validation_service/out/ ./out
```

2. replacing ip

```bash
sed 's#127.0.0.1#ec2-54-90-141-176.compute-1.amazonaws.com#g' ./out/testdata/fsc/nodes/test-node.0/client-config.yaml -i
```

```bash
GOGC=10000 nohup go run ./client/ -benchtime=30s -count=5 -workloads=token-validation-service -cpu=1,2,4,8,16,32,48,64 -numConn=1,2,4,8 2>&1 | tee out.txt &
```


# Prometheus Setup 
1. Download

```bash
sudo yum update -y && sudo yum install -y wget
wget https://github.com/prometheus/prometheus/releases/download/v2.52.0/prometheus-2.52.0.linux-amd64.tar.gz
tar xvf prometheus-2.52.0.linux-amd64.tar.gz
sudo mv prometheus-2.52.0.linux-amd64/prometheus /usr/local/bin/
sudo mv prometheus-2.52.0.linux-amd64/promtool /usr/local/bin/
```

2. Make config dir 

```bash
sudo mkdir /etc/prometheus
sudo mkdir /var/lib/prometheus
sudo cp prometheus-2.52.0.linux-amd64/prometheus.yml /etc/prometheus/
```

3. Configure Prometheus for node exporter
```bash
sudo nano /etc/prometheus/prometheus.yml
```
```yaml
scrape_configs:
  - job_name: "node"
    static_configs:
      - targets: ["localhost:9100"]
```    
4. Add Prometheus user
```bash
sudo useradd --no-create-home --shell /bin/false prometheus
sudo chown -R prometheus:prometheus /etc/prometheus
sudo chown -R prometheus:prometheus /var/lib/prometheus
sudo chown -R $(whoami):$(whoami) /var/lib/prometheus
```

4. Run Prometheus
```bash
prometheus \
  --config.file=/etc/prometheus/prometheus.yml \
  --storage.tsdb.path=/var/lib/prometheus
```

# Install Node Exporter 
```bash
wget https://github.com/prometheus/node_exporter/releases/latest/download/node_exporter-1.10.2.linux-amd64.tar.gz
tar xvf node_exporter-1.10.2.linux-amd64.tar.gz
cd node_exporter-1.10.2.linux-amd64
sudo mv node_exporter /usr/local/bin/
```
Run it
```bash 
node_exporter # see http://<EC2-IP>:9100/metrics
```
(verify scraping at: `http://<EC2-IP>:9090/targets`)


# Install Graphana
```bash
sudo yum install grafana -y
```
Start Graphana
```bash
sudo systemctl daemon-reexec
sudo systemctl start grafana-server
sudo systemctl enable grafana-server
```

Now a vailable at: `http://<EC2-IP>:3000`
user/pwd: `admin/admin`

## Setup dashboard
In Grafana UI > Go to Dashboards > Click Import  

Enter dashboard ID:`1860`  

In `http://<EC2-IP>:3000/connections/datasources`:  
Add prometheus > Save and Test 

if you get:
```bash
Post "http://127.0.0.1:9090/api/v1/query": dial tcp 127.0.0.1:9090: connect: permission denied - There was an error returned querying the Prometheus API.
```

Do `sudo setenforce 0`