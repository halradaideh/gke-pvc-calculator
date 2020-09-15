package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3"
	googlepb "github.com/golang/protobuf/ptypes/timestamp"
	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
	monitoringpb "google.golang.org/genproto/googleapis/monitoring/v3"

	// "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type DiskStatus struct {
	All       float64 `json:"all"`
	Used      float64 `json:"used"`
	Free      float64 `json:"free"`
	Avail     float64 `json:"avail"`
	Util      float64 `json:"util"`
	NameSpace string  `json:"namespace"`
	PvcName   string  `json:"pvname"`
}

func writeData(t string, pv string, value float64, project string, disk DiskStatus) {
	ctx := context.Background()

	// Creates a client.
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	// Sets your Google Cloud Platform project ID.
	projectID := project

	// Prepares an individual data point
	dataPoint := &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{
			EndTime: &googlepb.Timestamp{
				Seconds: time.Now().Unix(),
			},
		},
		Value: &monitoringpb.TypedValue{
			Value: &monitoringpb.TypedValue_DoubleValue{
				DoubleValue: value,
			},
		},
	}

	// Writes time series data.
	if err := client.CreateTimeSeries(ctx, &monitoringpb.CreateTimeSeriesRequest{
		Name: monitoring.MetricProjectPath(projectID),
		TimeSeries: []*monitoringpb.TimeSeries{
			{
				Metric: &metricpb.Metric{
					Type: "custom.googleapis.com/pvc/" + t,
					Labels: map[string]string{
						"pv":        pv,
						"pvc":       disk.PvcName,
						"namespace": disk.NameSpace,
					},
				},
				Resource: &monitoredrespb.MonitoredResource{
					Type: "global",
					Labels: map[string]string{
						"project_id": projectID,
					},
				},
				Points: []*monitoringpb.Point{
					dataPoint,
				},
			},
		},
	}); err != nil {
		log.Fatalf("Failed to write time series data: %v", err)
	}

	// Closes the client and flushes the data to Stackdriver.
	if err := client.Close(); err != nil {
		log.Fatalf("Failed to close client: %v", err)
	}

	fmt.Printf("Done writing time series data.\n")
	time.Sleep(10 * time.Second)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// disk usage of path/disk
func DiskUsage(path string, disk DiskStatus) DiskStatus {
	fs := syscall.Statfs_t{}
	err := syscall.Statfs(path, &fs)
	if err != nil {
		return DiskStatus{}
	}
	disk.All = float64(fs.Blocks * uint64(fs.Bsize))
	disk.Avail = float64(fs.Bavail * uint64(fs.Bsize))
	disk.Free = float64(fs.Bfree * uint64(fs.Bsize))
	disk.Used = disk.All - disk.Free
	disk.Util = (disk.Used / disk.All) * 100
	return disk
}

// Calculate persistent volumes' size for the ones mounted on the server
// /kmounts -> /var/lib/kubelet/plugins (external)
// Since full external path is /var/lib/kubelet/plugins/kubernetes.io/gce-pd/mounts/
// What we search inside is /kmounts/kubernetes.io/gce-pd/mounts/
func pvSizeCalc(pvMap map[string]DiskStatus) map[string]DiskStatus {
	var pvPath string = "/kmounts/kubernetes.io/gce-pd/mounts/"
	files, err := ioutil.ReadDir(pvPath)
	// While the server does not have any volumes, the folder will not exist, so ignore the error
	if err != nil {
		//log.Fatal(err)
	}

	for _, f := range files {
		if f.IsDir() {
			// Look for a folder name that contains 'pvc' and take as the name the last occurence till the end
			nameRe := regexp.MustCompile(`.*(pvc-.*)`)
			if nameRe.MatchString(f.Name()) {
				nameMatch := nameRe.FindStringSubmatch(f.Name())
				// pvMap[nameMatch[1]] = DiskUsage("/kmounts/kubernetes.io/gce-pd/mounts/" + f.Name())
				pvMap[nameMatch[1]] = DiskUsage("/kmounts/kubernetes.io/gce-pd/mounts/"+f.Name(), pvMap[nameMatch[1]])
			}
		}
	}
	return pvMap
}

func getPvcs() map[string]DiskStatus {

	var pvMap map[string]DiskStatus
	pvMap = make(map[string]DiskStatus)

	// create a kubernetes client from pod service account

	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	pvcs, err := clientset.CoreV1().PersistentVolumeClaims("").List(metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	for _, pvc := range pvcs.Items {
		fmt.Println(pvc.Name, pvc.Spec.VolumeName, pvc.Namespace)
		pvMap[pvc.Spec.VolumeName] = DiskMeta(pvc.Name, pvc.Namespace)
	}

	time.Sleep(10 * time.Second)

	return pvMap
}

func DiskMeta(pvname, namespace string) (disk DiskStatus) {
	disk.PvcName = pvname
	disk.NameSpace = namespace
	return
}

func main() {
	metricsFrequencySec, err := strconv.ParseInt(getEnv("DISC_CALC_FREQ_SECONDS", "60"), 10, 32) // default to 60 sec
	if err != nil {
		log.Fatal(err)
	}

	project, ok := os.LookupEnv("GCP_PROJECT")
	if !ok {
		log.Fatal("no ENV GCP_PROJECT defined for project name")
	}

	for {
		pvMap := pvSizeCalc(getPvcs())

		// todo : only select pvc that present on the node, will do a small hack for now
		// fmt.Println(pvMap)

		for pvname, pv := range pvMap {
			// hack
			if pv.All == 0 {
				continue
			}
			fmt.Println(pvname, pv.All, pv.Used, pv.Util)
			writeData("used", pvname, pv.Used, project, pv)
			writeData("size", pvname, pv.All, project, pv)
			writeData("util", pvname, pv.Util, project, pv)
		}

		fmt.Println("sleeping")
		time.Sleep(time.Duration(metricsFrequencySec) * time.Second)
	}

}
