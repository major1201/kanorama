package modules

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Storage struct {
	ModuleAbstract

	classRows        [][]string
	topClassRows     [][]string
	kindTopRows      [][]string
	namespaceTopRows [][]string
}

func (m Storage) Name() string {
	return "Storage"
}

func (m Storage) ID() string {
	return strings.ToLower(m.Name())
}

func (m Storage) Visible() bool {
	return true
}

func (m Storage) EnableByDefault() bool {
	return true
}

func (m *Storage) Run() error {
	clientset, err := newClientset()
	if err != nil {
		return err
	}
	ctx := m.getContext()

	scList, err := clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	pvcList, err := clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	pvList, err := clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	pvByName := make(map[string]*corev1.PersistentVolume, len(pvList.Items))
	pvClassCounts := make(map[string]int)
	pvClassBytes := make(map[string]int64)
	for i := range pvList.Items {
		pv := &pvList.Items[i]
		pvByName[pv.Name] = pv
		class := pvStorageClass(pv)
		pvClassCounts[class]++
		pvClassBytes[class] += pvStorageBytes(pv)
	}

	pvcClassCounts := make(map[string]int)
	pvcClassBytes := make(map[string]int64)
	kindBytes := make(map[string]int64)
	namespaceBytes := make(map[string]int64)
	var totalPVCBytes int64
	var totalPVCCount int

	owners := buildOwnerIndex(ctx, clientset)
	podByClaim := buildPodClaimIndex(m.getCache(), ctx)

	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		bytes := pvcStorageBytes(pvc)
		if bytes <= 0 {
			continue
		}

		class := pvcStorageClass(pvc, pvByName)
		pvcClassCounts[class]++
		pvcClassBytes[class] += bytes
		totalPVCBytes += bytes
		totalPVCCount++

		kind := resolveTopKind(pvc.OwnerReferences, owners, "")
		if kind == "" || kind == "PVC" {
			if pod, ok := podByClaim[pvc.Namespace+"/"+pvc.Name]; ok {
				kind = topLevelKind(pod, owners)
			} else {
				kind = "PVC"
			}
		}
		kindBytes[kind] += bytes

		namespaceBytes[pvc.Namespace] += bytes
	}

	m.classRows = buildStorageClassRows(scList, pvcClassCounts, pvcClassBytes, pvClassCounts, pvClassBytes)
	m.topClassRows = buildTopClassRows(pvcClassCounts, pvcClassBytes, int64(totalPVCCount))
	m.kindTopRows = buildStorageTopRows(kindBytes, totalPVCBytes, 5)
	m.namespaceTopRows = buildStorageTopRows(namespaceBytes, totalPVCBytes, 5)

	return nil
}

func (m Storage) Print(w io.Writer) error {
	var buf strings.Builder

	buf.WriteString("Storage Classes:\n")
	if len(m.classRows) == 0 {
		buf.WriteString("(none)\n")
	} else {
		renderTable(&buf, []string{"Name", "Provisioner", "ReclaimPolicy", "VolumeBindingMode", "PVCs", "PVC Storage", "PVs", "PV Storage"}, m.classRows)
	}

	writeStorageTopSection(&buf, "Top 5 Storage Classes by PVCs", []string{"Storage Class", "PVCs", "PVC Storage", "%"}, m.topClassRows)
	writeStorageTopSection(&buf, "Top 5 Kinds by PVC Storage", []string{"Kind", "Storage", "%"}, m.kindTopRows)
	writeStorageTopSection(&buf, "Top 5 Namespaces by PVC Storage", []string{"Namespace", "Storage", "%"}, m.namespaceTopRows)

	_, err := io.WriteString(w, buf.String())
	return err
}

func writeStorageTopSection(buf *strings.Builder, title string, headers []string, rows [][]string) {
	buf.WriteString(title + ":\n")
	if len(rows) == 0 {
		buf.WriteString("(none)\n")
		return
	}
	renderTable(buf, headers, rows)
}

func buildStorageClassRows(scList *storagev1.StorageClassList, pvcClassCounts map[string]int, pvcClassBytes map[string]int64, pvClassCounts map[string]int, pvClassBytes map[string]int64) [][]string {
	sort.Slice(scList.Items, func(i, j int) bool {
		if pvcClassBytes[scList.Items[i].Name] != pvcClassBytes[scList.Items[j].Name] {
			return pvcClassBytes[scList.Items[i].Name] > pvcClassBytes[scList.Items[j].Name]
		}
		return scList.Items[i].Name < scList.Items[j].Name
	})

	rows := make([][]string, 0, len(scList.Items))
	for i := range scList.Items {
		sc := &scList.Items[i]
		name := sc.Name
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			name += " (default)"
		}
		reclaim := "-"
		if sc.ReclaimPolicy != nil {
			reclaim = string(*sc.ReclaimPolicy)
		}
		binding := "-"
		if sc.VolumeBindingMode != nil {
			binding = string(*sc.VolumeBindingMode)
		}
		rows = append(rows, []string{
			name,
			sc.Provisioner,
			reclaim,
			binding,
			strconv.Itoa(pvcClassCounts[sc.Name]),
			formatBytes(pvcClassBytes[sc.Name]),
			strconv.Itoa(pvClassCounts[sc.Name]),
			formatBytes(pvClassBytes[sc.Name]),
		})
	}
	return rows
}

func buildTopClassRows(counts map[string]int, bytes map[string]int64, total int64) [][]string {
	rows := make([][]string, 0, 5)
	for _, class := range sortedIntKeys(counts) {
		if counts[class] == 0 {
			continue
		}
		rows = append(rows, []string{
			class,
			strconv.Itoa(counts[class]),
			formatBytes(bytes[class]),
			percentString(int64(counts[class]), total),
		})
		if len(rows) == 5 {
			break
		}
	}
	return rows
}

func buildStorageTopRows(values map[string]int64, total int64, limit int) [][]string {
	rows := make([][]string, 0, limit)
	for _, key := range sortedInt64Keys(values) {
		if values[key] == 0 {
			continue
		}
		rows = append(rows, []string{
			key,
			formatBytes(values[key]),
			percentString(values[key], total),
		})
		if len(rows) == limit {
			break
		}
	}
	return rows
}

func sortedInt64Keys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

func buildPodClaimIndex(cache *Cache, ctx context.Context) map[string]*corev1.Pod {
	index := make(map[string]*corev1.Pod)
	pods, err := cache.Pods(ctx)
	if err != nil {
		return index
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				index[pod.Namespace+"/"+vol.PersistentVolumeClaim.ClaimName] = pod
			}
		}
	}
	return index
}

func pvcStorageClass(pvc *corev1.PersistentVolumeClaim, pvByName map[string]*corev1.PersistentVolume) string {
	if pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
		return *pvc.Spec.StorageClassName
	}
	if pvc.Spec.VolumeName != "" {
		if pv, ok := pvByName[pvc.Spec.VolumeName]; ok {
			return pvStorageClass(pv)
		}
	}
	return "(none)"
}

func pvStorageClass(pv *corev1.PersistentVolume) string {
	if pv.Spec.StorageClassName != "" {
		return pv.Spec.StorageClassName
	}
	return "(none)"
}

func pvcStorageBytes(pvc *corev1.PersistentVolumeClaim) int64 {
	q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if !ok {
		return 0
	}
	return q.Value()
}

func pvStorageBytes(pv *corev1.PersistentVolume) int64 {
	q, ok := pv.Spec.Capacity[corev1.ResourceStorage]
	if !ok {
		return 0
	}
	return q.Value()
}
