package metrics

import (
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"

	log "github.com/sirupsen/logrus"
)

// Max number of metrics we want to keep.
const maxCollectionValues = 300

func (s *Service) collectCPUUtilization() {
	if len(s.metrics.CPUUtilizations) > maxCollectionValues {
		s.metrics.CPUUtilizations = s.metrics.CPUUtilizations[1:]
	}

	v, err := cpu.Percent(0, false)
	if err != nil {
		log.Errorln(err)
		return
	}

	// Default to zero but try to use the cumulative values of all the CPUs
	// if values exist.
	value := 0.0
	if len(v) > 0 {
		value = v[0]
	}

	metricValue := TimestampedValue{time.Now(), value}
	s.metrics.CPUUtilizations = append(s.metrics.CPUUtilizations, metricValue)
	s.cpuUsage.Set(metricValue.Value)
}

func (s *Service) collectRAMUtilization() {
	memoryUsage, err := mem.VirtualMemory()
	if err != nil || memoryUsage == nil {
		if err != nil {
			log.Errorln(err)
		}
		return
	}

	if len(s.metrics.RAMUtilizations) > maxCollectionValues {
		s.metrics.RAMUtilizations = s.metrics.RAMUtilizations[1:]
	}

	metricValue := TimestampedValue{time.Now(), memoryUsage.UsedPercent}
	s.metrics.RAMUtilizations = append(s.metrics.RAMUtilizations, metricValue)
}

func (s *Service) collectDiskUtilization() {
	path := "./"
	diskUse, err := disk.Usage(path)
	if err != nil || diskUse == nil {
		if err != nil {
			log.Errorln(err)
		}
		return
	}

	if len(s.metrics.DiskUtilizations) > maxCollectionValues {
		s.metrics.DiskUtilizations = s.metrics.DiskUtilizations[1:]
	}

	metricValue := TimestampedValue{time.Now(), diskUse.UsedPercent}
	s.metrics.DiskUtilizations = append(s.metrics.DiskUtilizations, metricValue)
}

// primeCPUBaseline triggers a throwaway non-blocking gopsutil CPU sample so the
// first real poll reports the recent interval rather than the since-boot average.
func primeCPUBaseline() {
	_, _ = cpu.Percent(0, false)
}
