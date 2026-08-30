package modules

var ModuleList = []Module{
	&Version{},
	&Auth{},
	&Nodes{},
	&Kubelet{},
	&Network{},
	&Ingresses{},
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
