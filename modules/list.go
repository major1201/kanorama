package modules

var ModuleList = []Module{
	&Version{},
	&Auth{},
	&Nodes{},
	&Kubelet{},
	&Network{},
	&Namespaces{},
	&Pods{},
	&DaemonSet{},
	&Storage{},
	&Helm{},
	&CRD{},
	&Webhooks{},
	&Certificates{},
	&Events{},
}
