env GOOS=linux GOARCH=amd64 go build

rsync -avz --progress central.yaml s1.encodeous.ca:~/central.yaml
rsync -avz --progress central.yaml s2.encodeous.ca:~/central.yaml
rsync -avz --progress central.yaml        10.5.0.7:~/central.yaml
rsync -avz --progress central.yaml        10.5.0.9:~/central.yaml
rsync -avz --progress central.yaml l1.encodeous.ca:~/central.yaml
rsync -avz --progress central.yaml l2.encodeous.ca:~/central.yaml

ssh -t s1.encodeous.ca "sudo systemctl restart nylon"
ssh -t s2.encodeous.ca "sudo systemctl restart nylon"
ssh -t        10.5.0.7 "sudo systemctl restart nylon"
ssh -t        10.5.0.9 "sudo systemctl restart nylon"
ssh -t l1.encodeous.ca "sudo systemctl restart nylon"
ssh -t l2.encodeous.ca "sudo systemctl restart nylon"

