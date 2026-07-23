systemctl stop teleport
# stop any running Teleport# processes
#pkill -f teleport
# remove any data under /var/lib/teleport, along with the directory itself
rm -rf /var/lib/teleport
rm -f /etc/teleport.yaml
apt remove teleport -y
rm -rf /usr/local/bin/tctl
rm -rf /usr/local/bin/tsh
rm -rf /usr/local/bin/teleport