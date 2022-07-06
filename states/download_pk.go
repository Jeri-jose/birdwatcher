package states

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/congqixia/birdwatcher/proto/v2.0/datapb"
	"github.com/manifoldco/promptui"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/spf13/cobra"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func getDownloadPKCmd(cli *clientv3.Client, basePath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download-pk",
		Short: "download pk column of a collection",
		RunE: func(cmd *cobra.Command, args []string) error {
			collectionID, err := cmd.Flags().GetInt64("id")
			if err != nil {
				return err
			}

			coll, err := getCollectionByID(cli, basePath, collectionID)
			if err != nil {
				return nil
			}

			var pkID int64 = -1

			for _, field := range coll.Schema.Fields {
				if field.IsPrimaryKey {
					pkID = field.FieldID
					break
				}
			}

			if pkID < 0 {
				fmt.Println("collection pk not found")
				return nil
			}

			segments, err := listSegments(cli, basePath, func(segment *datapb.SegmentInfo) bool {
				return segment.CollectionID == collectionID
			})

			if err != nil {
				return err
			}

			p := promptui.Prompt{
				Label: "BucketName",
			}
			bucketName, err := p.Run()
			if err != nil {
				return err
			}

			minioClient, exists, err := getMinioClient()
			if err != nil {
				fmt.Println("cannot get minio client", err.Error())
				return nil
			}

			if !exists {
				fmt.Printf("Bucket not exist\n")
				return nil
			}

			downloadPks(minioClient, bucketName, collectionID, pkID, segments)

			return nil
		},
	}

	cmd.Flags().Int64("id", 0, "collection id to display")
	return cmd
}

func getMinioClient() (*minio.Client, bool, error) {
	p := promptui.Prompt{Label: "Address"}
	address, err := p.Run()
	if err != nil {
		return nil, false, err
	}

	ssl := promptui.Select{
		Label: "Use SSL",
		Items: []string{"yes", "no"},
	}
	_, sslResult, err := ssl.Run()
	useSSL := false
	switch sslResult {
	case "yes":
		useSSL = true
	case "no":
		useSSL = false
	}

	p.Label = "Bucket Name"
	bucketName, err := p.Run()
	if err != nil {
		return nil, false, err
	}

	sl := promptui.Select{
		Label: "Select authentication method:",
		Items: []string{"IAM", "AK/SK"},
	}
	_, result, err := sl.Run()
	if err != nil {
		return nil, false, err
	}
	fmt.Println("Use authen: ", result)

	var cred *credentials.Credentials
	switch result {
	case "IAM":
		input := promptui.Prompt{
			Label: "IAM Endpoint",
		}

		iamEndpoint, err := input.Run()
		if err != nil {
			return nil, false, err
		}
		cred = credentials.NewIAM(iamEndpoint)
	case "AK/SK":
		p.Label = "AK"
		ak, err := p.Run()
		if err != nil {
			return nil, false, err
		}
		p.Label = "SK"
		sk, err := p.Run()
		if err != nil {
			return nil, false, err
		}

		cred = credentials.NewStaticV4(ak, sk, "")
	}

	minioClient, err := minio.New(address, &minio.Options{
		Creds:  cred,
		Secure: useSSL,
	})

	if err != nil {
		return nil, false, err
	}

	exists, err := minioClient.BucketExists(context.Background(), bucketName)
	if !exists {
		return nil, false, nil
	}

	return minioClient, true, nil
}

func downloadPks(cli *minio.Client, bucketName string, collID, pkID int64, segments []*datapb.SegmentInfo) {
	err := os.Mkdir(fmt.Sprintf("%d", collID), 0777)
	if err != nil {
		fmt.Println("Failed to create folder,", err.Error())
	}

	count := 0
	for _, segment := range segments {
		for _, fieldBinlog := range segment.Binlogs {
			if fieldBinlog.FieldID != pkID {
				continue
			}

			folder := fmt.Sprintf("%d/%d", collID, segment.ID)
			err := os.MkdirAll(folder, 0777)
			if err != nil {
				fmt.Println("Failed to create sub-folder", err.Error())
				return
			}

			for _, binlog := range fieldBinlog.Binlogs {
				obj, err := cli.GetObject(context.Background(), bucketName, binlog.GetLogPath(), minio.GetObjectOptions{})
				if err != nil {
					fmt.Println("failed to download file", bucketName, binlog.GetLogPath())
					return
				}

				name := path.Base(binlog.GetLogPath())

				f, err := os.Create(path.Join(folder, name))
				if err != nil {
					fmt.Println("failed to open file")
					return
				}
				w := bufio.NewWriter(f)
				r := bufio.NewReader(obj)
				io.Copy(w, r)
				count++
			}
		}
	}
	fmt.Printf("pk file download completed for collection :%d, %d file(s) downloaded", collID, count)

}
